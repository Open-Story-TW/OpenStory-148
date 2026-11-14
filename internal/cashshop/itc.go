package cashshop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// itcState is what one trading center connection remembers between
// requests: the list on screen, refreshed after every change.
type itcState struct {
	view     protocol.ITCListRequest
	sequence uint64
	ignored  map[byte]bool
}

// openITC sends the item trading center stage, the balance and the two side
// panels (等待販售 and 拍賣倉庫).
func (s *server) openITC(ctx context.Context, c *protocol.Conn, session *domain.CashSession, st *itcState) error {
	st.view = protocol.ITCListRequest{Tab: 1, Category: 1}
	if err := c.WritePacket(protocol.ITCOpen(session.Session.Character, session.AccountName, time.Now())); err != nil {
		return err
	}
	if err := s.sendITCBalance(ctx, c, session); err != nil {
		return err
	}
	return s.sendITCPanels(ctx, c, session)
}

func (s *server) sendITCBalance(ctx context.Context, c *protocol.Conn, session *domain.CashSession) error {
	session.Lease = s.currentLease()
	wallet, err := s.repo.ReadCashWallet(ctx, *session)
	if err != nil {
		return err
	}
	// The trading center trades in 楓葉點數 (its help page says so), which is
	// the wallet's second balance.
	return c.WritePacket(protocol.ITCQueryCash(wallet.Points))
}

func (s *server) itcRepo() (domain.ITCRepository, bool) {
	repo, ok := s.repo.(domain.ITCRepository)
	return repo, ok
}

func (s *server) sendITCPanels(ctx context.Context, c *protocol.Conn, session *domain.CashSession) error {
	repo, ok := s.itcRepo()
	if !ok {
		return nil
	}
	session.Lease = s.currentLease()
	selling, err := repo.ITCSelling(ctx, *session)
	if err != nil {
		return err
	}
	storage, err := repo.ITCStorage(ctx, *session)
	if err != nil {
		return err
	}
	if err = c.WritePacket(protocol.ITCSelling(selling)); err != nil {
		return err
	}
	return c.WritePacket(protocol.ITCStorage(storage))
}

// sendITCList answers the list on screen; its last byte releases the
// client's request gate.
func (s *server) sendITCList(ctx context.Context, c *protocol.Conn, session *domain.CashSession, req protocol.ITCListRequest) error {
	repo, ok := s.itcRepo()
	if !ok {
		return c.WritePacket(protocol.ITCEmptyList(req))
	}
	session.Lease = s.currentLease()
	page, err := repo.ITCList(ctx, *session, domain.ITCView{Tab: int(req.Tab), Type: int(req.Type), Page: int(req.Page)})
	if err != nil {
		return err
	}
	return c.WritePacket(protocol.ITCList(req, page))
}

// handleITC answers the item trading center's own requests. It reports
// whether op belonged to the trading center; the cash shop's requests are
// refused while the client is in it.
func (s *server) handleITC(ctx context.Context, c *protocol.Conn, session *domain.CashSession, op uint16, r *protocol.Reader, st *itcState) (bool, error) {
	switch op {
	case protocol.RecvITCQueryCash:
		return true, s.sendITCBalance(ctx, c, session)
	case protocol.RecvITCCharge:
		return true, c.WritePacket(protocol.ITCChargeResult())
	case protocol.RecvITCOperation:
		action := r.U8()
		if r.Err() != nil {
			return true, r.Err()
		}
		return true, s.itcAction(ctx, c, session, action, r, st)
	case protocol.RecvCashUpdate, protocol.RecvCashOperation, protocol.RecvCashCoupon, protocol.RecvCashGift, protocol.RecvNoteAction:
		// Cash shop requests have no meaning in the trading center.
		r.Skip(r.Remaining())
		return true, nil
	}
	return false, nil
}

func (s *server) itcAction(ctx context.Context, c *protocol.Conn, session *domain.CashSession, action byte, r *protocol.Reader, st *itcState) error {
	if action == protocol.ITCActionSearch {
		req, err := protocol.ReadITCSearch(r)
		if err != nil {
			return err
		}
		return s.sendITCSearch(ctx, c, session, req)
	}
	if action == protocol.ITCActionList {
		req, err := protocol.ReadITCListRequest(r)
		if err != nil {
			return err
		}
		st.view = req
		return s.sendITCList(ctx, c, session, req)
	}
	repo, ok := s.itcRepo()
	request := domain.ITCRequest{}
	var fail byte
	switch action {
	case protocol.ITCActionRegister:
		reg, err := protocol.ReadITCRegister(r)
		if err != nil {
			slog.Info("itc_register_malformed", "character", session.Session.CharacterID, "error", err.Error())
			return c.WritePacket(protocol.ITCResultCode(protocol.ITCResultRegisterFail, protocol.ITCCodeNotAllowed))
		}
		slog.Info("itc_register_request", "character", session.Session.CharacterID, "template", reg.TemplateID, "slot", reg.Slot, "quantity", reg.Quantity, "price", reg.Price, "days", reg.Days)
		request = domain.ITCRequest{Kind: "register", InvType: reg.TemplateID / 1000000, Slot: reg.Slot, TemplateID: reg.TemplateID, Quantity: reg.Quantity, Price: reg.Price}
		if reg.Days != 7 || (reg.ItemType == 1) != (request.InvType == 1) {
			return c.WritePacket(protocol.ITCResultCode(protocol.ITCResultRegisterFail, protocol.ITCCodeNotAllowed))
		}
		fail = protocol.ITCResultRegisterFail
	case protocol.ITCActionAuction:
		reg, err := protocol.ReadITCAuction(r)
		if err != nil {
			slog.Info("itc_auction_malformed", "character", session.Session.CharacterID, "error", err.Error())
			return c.WritePacket(protocol.ITCResultCode(protocol.ITCResultAuctionFail, protocol.ITCCodeUnknown))
		}
		slog.Info("itc_auction_request", "character", session.Session.CharacterID, "template", reg.TemplateID, "slot", reg.Slot, "quantity", reg.Quantity, "start", reg.StartPrice, "buy_now", reg.BuyNowPrice, "hours", reg.Hours, "step", reg.BidStep)
		request = domain.ITCRequest{Kind: "auction", InvType: reg.TemplateID / 1000000, Slot: reg.Slot, TemplateID: reg.TemplateID, Quantity: reg.Quantity, Price: reg.StartPrice, BuyNowPrice: reg.BuyNowPrice, BidStep: reg.BidStep, Hours: reg.Hours}
		if (reg.ItemType == 1) != (request.InvType == 1) {
			return c.WritePacket(protocol.ITCResultCode(protocol.ITCResultAuctionFail, protocol.ITCCodeUnknown))
		}
		fail = protocol.ITCResultAuctionFail
	case protocol.ITCActionBid:
		bid, err := protocol.ReadITCBid(r)
		if err != nil {
			return err
		}
		request = domain.ITCRequest{Kind: "bid", ListingID: bid.ListingID, Price: bid.Base + bid.Raise}
		fail = protocol.ITCResultBidFail
	case protocol.ITCActionBuyNow:
		request.Kind, fail = "buy_now", protocol.ITCResultBuyNowFail
	case protocol.ITCActionWanted:
		want, err := protocol.ReadITCWanted(r)
		if err != nil {
			return err
		}
		slog.Info("itc_wanted_request", "character", session.Session.CharacterID, "template", want.TemplateID, "price", want.Price, "quantity", want.Quantity)
		request = domain.ITCRequest{Kind: "wanted", TemplateID: want.TemplateID, Price: want.Price, Quantity: want.Quantity, Comment: want.Comment}
		fail = protocol.ITCResultWantedFail
	case protocol.ITCActionFill:
		fill, err := protocol.ReadITCFill(r)
		if err != nil {
			return err
		}
		request = domain.ITCRequest{Kind: "fill", ListingID: fill.ListingID, InvType: fill.InvType, Slot: fill.Slot}
		fail = protocol.ITCResultAuctionFail
	case protocol.ITCActionMyLeft:
		// 購買 on a 等待購買 row takes an item sold to the own request.
		request.Kind, fail = "accept", protocol.ITCResultAcceptFail
	case protocol.ITCActionMyRight:
		// 取消 on a 等待購買 row sends such an item back, or cancels the
		// own buy request itself.
		request.Kind, fail = "withdraw", protocol.ITCResultWithdrawFail
	case protocol.ITCActionBuy, protocol.ITCActionBuyCart:
		request.Kind, fail = "buy", protocol.ITCResultBuyFail
	case protocol.ITCActionCancel:
		request.Kind, fail = "cancel", protocol.ITCResultCancelFail
	case protocol.ITCActionTake:
		request.Kind, fail = "take", protocol.ITCResultTakeFail
	case protocol.ITCActionCartAdd:
		request.Kind, fail = "cart_add", protocol.ITCResultCartDuplicate
	case protocol.ITCActionCartRemove:
		request.Kind, fail = "cart_remove", protocol.ITCResultCartFail
	default:
		if st.ignored == nil {
			st.ignored = make(map[byte]bool)
		}
		if !st.ignored[action] {
			st.ignored[action] = true
			slog.Info("itc_action_pending", "action", action, "bytes", r.Remaining(), "character", session.Session.CharacterID)
		}
		r.Skip(r.Remaining())
		return c.WritePacket(protocol.ITCFailure(protocol.ITCCodeUnknown))
	}
	if request.Kind != "register" && request.Kind != "auction" && request.Kind != "bid" && request.Kind != "wanted" && request.Kind != "fill" {
		id, err := protocol.ReadITCListingID(r)
		if err != nil {
			return err
		}
		request.ListingID = id
	}
	if !ok {
		return c.WritePacket(protocol.ITCResultCode(fail, protocol.ITCCodeUnknown))
	}
	st.sequence++
	session.Lease = s.currentLease()
	request.Session = *session
	request.OperationID = fmt.Sprintf("itc/%s/%d/%d", session.Session.SessionID, session.Session.Generation, st.sequence)
	opCtx, done := context.WithTimeout(ctx, 4*time.Second)
	result, err := repo.ChangeITC(opCtx, request)
	done()
	if err != nil {
		slog.Info("itc_rejected", "kind", request.Kind, "listing", request.ListingID, "character", session.Session.CharacterID, "error", err.Error())
		if errors.Is(err, domain.ErrCashLimit) && (request.Kind == "register" || request.Kind == "auction") {
			if err = c.WritePacket(protocol.ITCLimitFailure(domain.ITCSellingLimit)); err != nil {
				return err
			}
		} else if err = c.WritePacket(protocol.ITCResultCode(fail, itcFailureCode(request.Kind, err))); err != nil {
			return err
		}
		return s.refreshITC(ctx, c, session, st)
	}
	if len(result.Inventory.Changes) > 0 {
		session.Session.Character = result.Inventory.Character
		if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
			return err
		}
	}
	var done1 []byte
	switch request.Kind {
	case "register":
		session.Session.Character = result.Inventory.Character
		if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, false)); err != nil {
			return err
		}
		done1 = protocol.ITCResult(protocol.ITCResultRegistered)
	case "auction":
		session.Session.Character = result.Inventory.Character
		if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, false)); err != nil {
			return err
		}
		done1 = protocol.ITCResult(protocol.ITCResultAuctioned)
	case "buy":
		done1 = protocol.ITCResult(protocol.ITCResultBought)
	case "buy_now":
		done1 = protocol.ITCResult(protocol.ITCResultBoughtNow)
	case "wanted":
		session.Session.Character = result.Inventory.Character
		if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, false)); err != nil {
			return err
		}
		done1 = protocol.ITCResult(protocol.ITCResultWanted)
	case "fill":
		done1 = protocol.ITCResult(protocol.ITCResultAuctioned)
	case "withdraw":
		done1 = protocol.ITCResult(protocol.ITCResultWithdrawn)
	case "accept":
		done1 = protocol.ITCResult(protocol.ITCResultAccepted)
	case "cancel":
		done1 = protocol.ITCResult(protocol.ITCResultCancelled)
	case "take":
		invType, slot := result.Listing.Item.InvType, 0
		for _, change := range result.Inventory.Changes {
			slot = change.Slot
		}
		done1 = protocol.ITCTaken(invType, slot)
	case "cart_add":
		done1 = protocol.ITCResult(protocol.ITCResultCartAdded)
	case "cart_remove":
		done1 = protocol.ITCResult(protocol.ITCResultCartRemoved)
	}
	slog.Info("itc_changed", "kind", request.Kind, "listing", max(request.ListingID, result.Listing.ID), "character", session.Session.CharacterID)
	if done1 != nil {
		// A bid has no message of its own; the refreshed list shows it.
		if err = c.WritePacket(done1); err != nil {
			return err
		}
	}
	if err = c.WritePacket(protocol.ITCQueryCash(result.Wallet.Points)); err != nil {
		return err
	}
	return s.refreshITC(ctx, c, session, st)
}

// refreshITC resends the side panels and the list on screen, which also
// releases the client's request gate.
func (s *server) refreshITC(ctx context.Context, c *protocol.Conn, session *domain.CashSession, st *itcState) error {
	if err := s.sendITCPanels(ctx, c, session); err != nil {
		return err
	}
	return s.sendITCList(ctx, c, session, st.view)
}

func itcFailureCode(kind string, err error) byte {
	switch {
	case errors.Is(err, domain.ErrITCMesos):
		return protocol.ITCCodeMesos
	case errors.Is(err, domain.ErrITCHasBids):
		return protocol.ITCCodeHasBids
	case errors.Is(err, domain.ErrAlreadyExists) && kind == "bid":
		return protocol.ITCCodeRepeatBid
	case errors.Is(err, domain.ErrInsufficient):
		switch kind {
		case "register", "auction":
			return protocol.ITCCodeMesos
		case "take":
			return protocol.ITCCodeNoSpace
		}
		return protocol.ITCCodePoints
	case errors.Is(err, domain.ErrStaleOwner), errors.Is(err, domain.ErrNotFound) && kind != "register":
		return protocol.ITCCodeGone
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrInvalidInput):
		if kind == "register" {
			return protocol.ITCCodeNotAllowed
		}
	}
	return protocol.ITCCodeUnknown
}

// sendITCSearch answers a search (action 5): by the lister's character name,
// or by item name through String.wz.
func (s *server) sendITCSearch(ctx context.Context, c *protocol.Conn, session *domain.CashSession, req protocol.ITCSearchRequest) error {
	list := protocol.ITCListRequest{Tab: req.Tab, Type: req.Type}
	repo, ok := s.itcRepo()
	if !ok || strings.TrimSpace(req.Keyword) == "" {
		return c.WritePacket(protocol.ITCSearchList(list, domain.ITCPage{}))
	}
	view := domain.ITCView{Tab: int(req.Tab), Type: int(req.Type), Search: true}
	if req.ByItemName {
		items, err := content.SearchItems(os.Getenv("MAPLEGO_WZ_ROOT"), req.Keyword, 500)
		if err != nil && len(items) == 0 {
			slog.Warn("itc_search_items_unavailable", "error", err.Error())
		}
		for _, item := range items {
			view.Templates = append(view.Templates, item.ID)
		}
	} else {
		view.Name = strings.TrimSpace(req.Keyword)
	}
	session.Lease = s.currentLease()
	page, err := repo.ITCList(ctx, *session, view)
	if err != nil {
		return err
	}
	return c.WritePacket(protocol.ITCSearchList(list, page))
}
