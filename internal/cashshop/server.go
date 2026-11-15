package cashshop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

type server struct {
	mu       sync.RWMutex
	lease    domain.ServiceLease
	cfg      platform.Config
	repo     domain.CashRepository
	products []domain.CashProduct
	// live and orphanSince are guarded by mu; see handoff_recovery.go.
	live        map[cashSessionKey]int
	orphanSince map[cashSessionKey]time.Time
}

func (s *server) currentLease() domain.ServiceLease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lease
}

func Run(parent context.Context, cfg platform.Config, repository domain.Repository, health *platform.Health) error {
	repo, ok := repository.(domain.CashRepository)
	if !ok {
		return fmt.Errorf("cashshop repository is unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	lease, err := platform.AcquireLease(ctx, "cashshop", platform.LeaseWait, func(ctx context.Context) (domain.ServiceLease, error) {
		return repo.AcquireCashLease(ctx, cfg.InstanceID)
	})
	if err != nil {
		return err
	}
	s := &server{lease: lease, cfg: cfg, repo: repo}
	health.Handle("/admin/cash", http.HandlerFunc(s.adminCash))
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = repo.ReleaseCashLease(cleanup, s.currentLease())
	}()
	s.products, err = content.CashProducts()
	if err != nil {
		return err
	}
	g := platform.NewTCPGroup()
	defer g.Close()
	for _, host := range cfg.ListenHosts() {
		if err = g.Listen(ctx, net.JoinHostPort(host, strconv.Itoa(cfg.CashshopPort)), s.handle); err != nil {
			return err
		}
	}
	health.Detail("cashshop_port", cfg.CashshopPort)
	health.Detail("purchases_enabled", true)
	health.Detail("world_transfers_enabled", true)
	health.Detail("epoch", lease.Epoch)
	health.Ready(true)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, done := context.WithTimeout(ctx, 3*time.Second)
				next, e := repo.RenewCashLease(renewCtx, s.currentLease())
				done()
				if e != nil {
					slog.Warn("cashshop_lease_failed", "error", e)
					health.Ready(false)
					cancel()
					return
				}
				s.mu.Lock()
				s.lease = next
				s.mu.Unlock()
				reclaimCtx, reclaimDone := context.WithTimeout(ctx, 3*time.Second)
				s.reclaimOrphanSessions(reclaimCtx, time.Now())
				reclaimDone()
			}
		}
	}()
	<-ctx.Done()
	health.Ready(false)
	return nil
}

func (s *server) refresh(ctx context.Context, c *protocol.Conn, session *domain.CashSession) error {
	session.Lease = s.currentLease()
	var expired []domain.CashItem
	if expiry, ok := s.repo.(domain.CashLockerExpiryRepository); ok {
		var err error
		if expired, err = expiry.ExpireCashLocker(ctx, *session); err != nil {
			return err
		}
	}
	wallet, err := s.repo.ReadCashWallet(ctx, *session)
	if err != nil {
		return err
	}
	items, err := s.repo.ReadCashLocker(ctx, *session)
	if err != nil {
		return err
	}
	// Like Legacy doCSPackets: list the just-expired records, then remove each
	// with the native expiry notice. The native notice names the item from the
	// client's locker, so an unlisted serial must not be announced.
	expired = expired[:min(len(expired), max(0, 100-len(items)))]
	packets := [][]byte{protocol.CashLocker(append(items, expired...)), protocol.CashWalletPacket(wallet), protocol.EnableCashshop()}
	for _, item := range expired {
		packets = append(packets, protocol.CashItemExpired(item.ID))
	}
	for _, packet := range packets {
		if err = c.WritePacket(packet); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) handle(ctx context.Context, raw net.Conn) {
	c, err := protocol.NewServerConn(raw)
	if err != nil {
		return
	}
	defer c.Close()
	c.SetTimeouts(15*time.Minute, 5*time.Second)
	ip, _, err := net.SplitHostPort(raw.RemoteAddr().String())
	if err != nil {
		return
	}
	var session *domain.CashSession
	handedOff := false
	sequence := uint64(0)
	ignoredOpcodes := make(map[uint16]bool)
	itc := &itcState{}
	defer func() {
		if session != nil {
			s.untrackLive(*session)
		}
		if session != nil && !handedOff {
			cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			_ = s.repo.ReleaseCashCharacter(cleanup, *session)
		}
	}()
	for {
		packet, err := c.ReadPacket()
		if err != nil {
			return
		}
		r := protocol.NewReader(packet)
		op := r.U16()
		if session != nil && session.ITC {
			handled, e := s.handleITC(ctx, c, session, op, r, itc)
			if e != nil {
				return
			}
			if handled {
				continue
			}
		}
		switch op {
		case protocol.RecvHello:
			if r.U8() != 6 || r.U16() != protocol.Version || r.U16() != 1 || r.Err() != nil {
				return
			}
			if c.WritePacket(protocol.ShowMapleStory()) != nil {
				return
			}
		case protocol.RecvLoggedIn:
			if session != nil {
				return
			}
			handle := int64(r.I32())
			if r.Err() != nil || handle < 1 {
				return
			}
			claim, done := context.WithTimeout(ctx, 5*time.Second)
			value, e := s.repo.ConsumeCashTransfer(claim, handle, ip, s.currentLease())
			done()
			if e != nil {
				slog.Warn("cashshop_transfer_rejected", "error", e)
				return
			}
			session = &value
			s.trackLive(value)
			if value.ITC {
				if s.openITC(ctx, c, session, itc) != nil {
					return
				}
				slog.Info("character_entered_itc", "character", value.Session.CharacterID, "epoch", value.Lease.Epoch)
				continue
			}
			for _, p := range [][]byte{protocol.CashOpen(value.Session.Character), protocol.CashInfo(value.AccountName, s.products)} {
				if c.WritePacket(p) != nil {
					return
				}
			}
			if s.refreshSocial(ctx, c, session) != nil || s.refresh(ctx, c, session) != nil {
				return
			}
			slog.Info("character_entered_cashshop", "character", value.Session.CharacterID, "epoch", value.Lease.Epoch)
		case protocol.RecvPong, protocol.RecvClientStart:
		case protocol.RecvCashUpdate:
			if session == nil {
				return
			}
			if s.refreshSocial(ctx, c, session) != nil || s.refresh(ctx, c, session) != nil {
				return
			}
		case protocol.RecvCashCoupon, protocol.RecvCashGift:
			if session == nil {
				return
			}
			session.Lease = s.currentLease()
			var request domain.CashFeatureRequest
			var e error
			if op == protocol.RecvCashCoupon {
				request, e = protocol.ReadNativeCashCoupon(r)
			} else {
				request, e = protocol.ReadNativeDirectGift(r)
				if product, lookupErr := content.CashProduct(request.SN); lookupErr == nil && product.TemplateID/10000 == 910 {
					request.Kind = "gift_package"
				}
			}
			if e != nil {
				slog.Info("cashshop_feature_malformed", "opcode", op, "error", e.Error())
				code := reasonGiftInvalidInput
				if op == protocol.RecvCashCoupon {
					code = cashCouponFailure(e)
				}
				_ = c.WritePacket(protocol.CashFailure(code))
				continue
			}
			sequence++
			request.Session = *session
			request.OperationID = fmt.Sprintf("cash/%s/%d/%d", session.Session.SessionID, session.Session.Generation, sequence)
			if s.executeCashFeature(ctx, c, session, request) != nil {
				return
			}
		case protocol.RecvCashOperation:
			if session == nil {
				return
			}
			session.Lease = s.currentLease()
			action := r.U8()
			if r.Err() != nil {
				return
			}
			if action == 46 {
				sn, e := protocol.ReadNativeCashPurchaseRecord(r)
				if e != nil {
					return
				}
				history, ok := s.repo.(domain.CashPurchaseHistoryRepository)
				if !ok {
					return
				}
				queryCtx, done := context.WithTimeout(ctx, 3*time.Second)
				purchased, e := history.ReadCashPurchaseRecord(queryCtx, *session, sn)
				done()
				if e != nil {
					return
				}
				if c.WritePacket(protocol.CashPurchaseRecord(sn, purchased)) != nil {
					return
				}
				continue
			}
			if action == 60 || action == 61 {
				// Like Legacy (no-op + doCSPackets): the gift was already claimed
				// into the locker; the client only needs its request flag cleared.
				serial, e := protocol.ReadNativeCashGiftAck(r)
				if e != nil {
					return
				}
				slog.Info("cashshop_gift_acknowledged", "action", action, "serial", serial, "character", session.Session.CharacterID)
				if s.refresh(ctx, c, session) != nil {
					return
				}
				continue
			}
			if !protocol.CashActionWireReady(action) {
				// Retain durable APIs, but never mutate assets from unverified
				// native layouts. An unsupported action is not insufficient funds.
				slog.Warn("cashshop_native_action_pending", "action", action, "bytes", r.Remaining())
				if c.WritePacket(protocol.CashFailure(reasonUnknown)) != nil {
					return
				}
				continue
			}
			sequence++
			operation := fmt.Sprintf("cash/%s/%d/%d", session.Session.SessionID, session.Session.Generation, sequence)
			operationCtx, done := context.WithTimeout(ctx, 4*time.Second)
			switch action {
			case 4, 34:
				request, e := protocol.ReadNativeCashGift(action, r)
				if e != nil {
					_ = c.WritePacket(protocol.CashFailure(reasonGiftInvalidInput))
					done()
					continue
				}
				request.Session = *session
				request.OperationID = operation
				e = s.executeCashFeature(operationCtx, c, session, request)
				done()
				if e != nil {
					return
				}
				continue
			case 5, 33:
				features, ok := s.repo.(domain.CashFeaturesRepository)
				if !ok {
					done()
					return
				}
				request := domain.CashFeatureRequest{Session: *session, OperationID: operation}
				switch action {
				case 0:
					r.Skip(2)
					request.Kind = "coupon"
					request.Coupon = r.String()
				case 5:
					request.Kind = "wishlist"
					for i := range request.Wishlist {
						request.Wishlist[i] = int(r.I32())
					}
				case 33:
					r.Skip(1)
					request.Kind = "package"
					request.SN = int(r.I32())
				case 4, 34:
					request.Kind = "gift"
					if action == 34 {
						request.Kind = "gift_package"
					}
					request.PIN = r.String()
					request.SN = int(r.I32())
					if action == 4 {
						r.Skip(1)
					}
					request.Recipient = r.String()
					request.Message = r.String()
				}
				if r.Err() != nil {
					done()
					return
				}
				result, e := features.ChangeCashFeature(operationCtx, request)
				if e != nil {
					if errors.Is(e, domain.ErrCashLimit) {
						_ = c.WritePacket(protocol.CashLimitFailure(request.SN))
					} else {
						_ = c.WritePacket(protocol.CashFailure(cashFeatureFailure(request.Kind, e)))
					}
				} else {
					if result.Character.ID > 0 {
						session.Session.Character = result.Character
					}
					if len(result.Changes) > 0 {
						_ = c.WritePacket(protocol.ModifyInventory(result.Changes))
					}
					switch action {
					case 0:
						_ = c.WritePacket(protocol.CashCouponRedeemed(result))
					case 5:
						_ = c.WritePacket(protocol.CashWishlistItems(result.Wishlist, true))
					case 33:
						_ = c.WritePacket(protocol.CashPackageBought(result.Items))
					case 4, 34:
						_ = c.WritePacket(protocol.CashGiftSent(result))
					}
				}
			case 3:
				request, e := protocol.ReadNativeCashPurchase(r)
				if e != nil {
					_ = c.WritePacket(protocol.CashFailure(reasonUnknown))
					break
				}
				request.Session, request.OperationID = *session, operation
				result, e := s.repo.PurchaseCash(operationCtx, request)
				if e != nil {
					if errors.Is(e, domain.ErrCashLimit) {
						_ = c.WritePacket(protocol.CashLimitFailure(request.SN))
					} else {
						_ = c.WritePacket(protocol.CashFailure(cashPurchaseFailure(e)))
					}
				} else {
					_ = c.WritePacket(protocol.CashPurchased(result.Item))
				}
			case 14, 15:
				request, e := protocol.ReadNativeCashMove(action, r)
				if e != nil {
					_ = c.WritePacket(protocol.CashFailure(reasonUnknown))
					break
				}
				request.Session, request.OperationID = *session, operation
				result, e := s.repo.MoveCashItem(operationCtx, request)
				if e != nil {
					_ = c.WritePacket(protocol.CashFailure(cashMoveFailure(request.ToLocker, e)))
				} else {
					session.Session.Character = result.Character
					if action == 14 {
						_ = c.WritePacket(protocol.CashFromLocker(result.Item, result.Character))
					} else {
						_ = c.WritePacket(protocol.CashToLocker(result.Item))
					}
				}
			default:
				_ = c.WritePacket(protocol.CashFailure(reasonUnknown))
			}
			done()
			if s.refresh(ctx, c, session) != nil {
				return
			}
		case protocol.RecvChangeMap:
			if session == nil {
				return
			}
			session.Lease = s.currentLease()
			world, ok := s.cfg.World(session.Session.Lease.WorldID)
			if !ok || session.ReturnChannel < 1 || session.ReturnChannel > world.ChannelCount {
				_ = c.WritePacket(protocol.ServerBlocked(2))
				_ = c.WritePacket(protocol.EnableCashshop())
				continue
			}
			returnCtx, done := context.WithTimeout(ctx, 3*time.Second)
			transfer, e := s.repo.BeginCashReturn(returnCtx, *session, ip)
			done()
			if e != nil {
				_ = c.WritePacket(protocol.ServerBlocked(2))
				_ = c.WritePacket(protocol.EnableCashshop())
				continue
			}
			handedOff = true
			if c.WritePacket(protocol.ChannelChange(domain.AdvertisedHost(raw.LocalAddr(), world.Host), world.ChannelBasePort+transfer.ChannelID-1)) == nil {
				awaitRedirectDisconnect(c)
			}
			return
		case protocol.RecvNoteAction:
			if session == nil {
				return
			}
			// Legacy drops NOTE_ACTION inside the cash shop (no memo delivery);
			// the follow-up action61 still gets the normal refresh.
			note, e := protocol.ReadNativeGiftNote(r)
			if e != nil {
				slog.Info("cashshop_gift_note_malformed", "bytes", len(packet)-2)
				continue
			}
			slog.Info("cashshop_gift_note_dropped", "character", session.Session.CharacterID, "to", note.To, "fame", note.Fame, "serial", note.Serial)
		case protocol.RecvClientError:
			slog.Warn("cashshop_client_error", "bytes", r.Remaining())
		default:
			if session == nil {
				return
			}
			// The client also emits non-purchase notifications on entry. Do not
			// invent a financial failure in response to an unrelated opcode.
			if !ignoredOpcodes[op] {
				ignoredOpcodes[op] = true
				slog.Info("cashshop_ignored_opcode", "opcode", fmt.Sprintf("0x%x", op), "bytes", r.Remaining())
			}
		}
		if r.Err() != nil {
			return
		}
	}
}

func (s *server) refreshSocial(ctx context.Context, c *protocol.Conn, session *domain.CashSession) error {
	features, ok := s.repo.(domain.CashFeaturesRepository)
	if !ok {
		return c.WritePacket(protocol.CashGifts())
	}
	session.Lease = s.currentLease()
	gifts, err := features.ClaimCashGifts(ctx, *session)
	if err != nil {
		if !errors.Is(err, domain.ErrInsufficient) {
			return err
		}
		if err = c.WritePacket(protocol.CashFailure(reasonLockerFull)); err != nil {
			return err
		}
	}
	if err = c.WritePacket(protocol.CashGiftItems(gifts)); err != nil {
		return err
	}
	wishlist, err := features.ReadCashWishlist(ctx, *session)
	if err != nil {
		return err
	}
	return c.WritePacket(protocol.CashWishlistItems(wishlist, false))
}

func (s *server) executeCashFeature(parent context.Context, c *protocol.Conn, session *domain.CashSession, request domain.CashFeatureRequest) error {
	features, ok := s.repo.(domain.CashFeaturesRepository)
	if !ok {
		return c.WritePacket(protocol.CashFailure(cashFeatureFailure(request.Kind, domain.ErrInvalidInput)))
	}
	ctx, done := context.WithTimeout(parent, 4*time.Second)
	defer done()
	result, err := features.ChangeCashFeature(ctx, request)
	if err != nil {
		code := cashFeatureFailure(request.Kind, err)
		slog.Info("cashshop_feature_rejected", "kind", request.Kind, "sn", request.SN, "character", session.Session.CharacterID, "reason", code, "error", err.Error())
		if errors.Is(err, domain.ErrCashLimit) {
			return c.WritePacket(protocol.CashLimitFailure(request.SN))
		}
		if err = c.WritePacket(protocol.CashFailure(code)); err != nil {
			return err
		}
		return s.refresh(parent, c, session)
	}
	if result.Character.ID > 0 {
		session.Session.Character = result.Character
	}
	if len(result.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
			return err
		}
	}
	var packet []byte
	switch request.Kind {
	case "coupon":
		packet = protocol.CashCouponRedeemed(result)
	case "gift", "gift_package":
		packet = protocol.CashGiftSent(result)
	default:
		return c.WritePacket(protocol.CashFailure(reasonUnknown))
	}
	if err = c.WritePacket(packet); err != nil {
		return err
	}
	return s.refresh(parent, c, session)
}

func awaitRedirectDisconnect(c *protocol.Conn) {
	deadline := time.Now().Add(30 * time.Second)
	for range 64 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		c.SetTimeouts(remaining, 5*time.Second)
		if _, err := c.ReadPacket(); err != nil {
			return
		}
	}
}
