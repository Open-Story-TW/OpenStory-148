package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// openParcel opens the 宅配 window. The script host calls it for the parcel
// NPC after checking the NPC; fromNotice is a quick parcel's arrival notice.
func (s *Server) openParcel(ctx context.Context, live *liveCharacter, c packetSink, npcID int, fromNotice bool) error {
	if s.economyDialogs == nil || s.tradeActive(live) {
		return domain.ErrForbidden
	}
	repo, ok := s.repo.(domain.ParcelRepository)
	if !ok {
		return domain.ErrForbidden
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	box, err := repo.OpenParcels(ctx, session)
	if err != nil {
		return err
	}
	dialog, err := newEconomyDialog(live, "parcel", npcID)
	if err != nil {
		return err
	}
	s.setEconomyDialog(live, dialog)
	// The delivery time drawn on the send tab follows the setting in force.
	if err = c.WritePacket(protocol.ClientParcelPacket()); err != nil {
		return err
	}
	return c.WritePacket(protocol.OpenParcels(box, fromNotice))
}

// handleParcel is RecvParcel (0x70). Every request the window sends leaves
// it busy until a result code or a removal comes back, so each path answers.
func (s *Server) handleParcel(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	mode := r.U8()
	if r.Err() != nil {
		return protocol.ErrMalformed
	}
	if mode == 0 {
		// The arrival notice of a quick parcel opens the box anywhere.
		r.Skip(r.Remaining())
		repo, ok := s.repo.(domain.ParcelRepository)
		if !ok || s.getEconomyDialog(live, "parcel") != nil {
			return c.WritePacket(protocol.EnableActions())
		}
		live.mu.Lock()
		session := live.session
		live.mu.Unlock()
		if quick, err := repo.HasQuickParcel(ctx, session); err != nil || !quick {
			return c.WritePacket(protocol.EnableActions())
		}
		if err := s.openParcel(ctx, live, c, 0, true); err != nil {
			return c.WritePacket(protocol.EnableActions())
		}
		return nil
	}
	dialog := s.getEconomyDialog(live, "parcel")
	if dialog == nil {
		r.Skip(r.Remaining())
		if mode == 8 {
			return nil
		}
		return c.WritePacket(protocol.ParcelResult(protocol.ParcelBadRequest))
	}
	dialog.mu.Lock()
	defer dialog.mu.Unlock()
	switch mode {
	case 3:
		request, err := protocol.ReadParcelSend(r)
		if err != nil {
			return err
		}
		return s.sendParcel(ctx, live, c, dialog, request)
	case 5, 6:
		id := int64(r.I32())
		if r.Err() != nil || r.Remaining() != 0 {
			return protocol.ErrMalformed
		}
		return s.takeParcel(ctx, live, c, dialog, id, mode == 6)
	case 8:
		r.Skip(r.Remaining())
		s.closeEconomyDialog(live)
		return c.WritePacket(protocol.EnableActions())
	default:
		r.Skip(r.Remaining())
		return c.WritePacket(protocol.ParcelResult(protocol.ParcelBadRequest))
	}
}

func (s *Server) sendParcel(ctx context.Context, live *liveCharacter, c packetSink, dialog *economyDialog, q protocol.ParcelSendRequest) error {
	repo, ok := s.repo.(domain.ParcelRepository)
	if !ok {
		return c.WritePacket(protocol.ParcelResult(protocol.ParcelUnknownError))
	}
	if q.InvType < 0 || q.InvType > 4 || q.Mesos < 0 || (q.InvType == 0 && q.Mesos == 0) ||
		(q.InvType > 0 && (q.Slot < 1 || q.Quantity < 1)) || protocol.Big5Len(q.Recipient) < 1 || protocol.Big5Len(q.Recipient) > 15 ||
		(q.Quick && (protocol.Big5Len(q.Message) < 0 || protocol.Big5Len(q.Message) > domain.ParcelMaxMessage)) {
		return c.WritePacket(protocol.ParcelResult(protocol.ParcelBadRequest))
	}
	dialog.sequence++
	req := domain.ParcelSend{OperationID: fmt.Sprintf("%s/%d", dialog.id, dialog.sequence), InvType: q.InvType, Slot: q.Slot, Quantity: q.Quantity, Mesos: q.Mesos, Recipient: q.Recipient, Quick: q.Quick, Message: q.Message, CouponSlot: q.CouponSlot}
	live.persistMu.Lock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	result, err := repo.SendParcel(ctx, req)
	live.persistMu.Unlock()
	if err != nil {
		return s.parcelFailure(live, c, err, parcelSendCode(err))
	}
	s.applyInventoryResult(live, result.Inventory.Character)
	if len(result.Inventory.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
			return err
		}
	}
	if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, true)); err != nil {
		return err
	}
	if err = c.WritePacket(protocol.ParcelResult(protocol.ParcelSent)); err != nil {
		return err
	}
	s.announceParcel(result.Parcel)
	return nil
}

// announceParcel shows 「宅配物件抵達！」 to the receiver when the parcel
// arrives, if the receiver is in this world then. A parcel still in transit
// waits for its arrival time; the notice is not kept across a restart, and
// the parcel itself never depends on it.
func (s *Server) announceParcel(p domain.Parcel) {
	notify := func() {
		if peer, ok := s.guildSpace().peer(p.ReceiverID); ok {
			_ = peer.sink.WritePacket(protocol.ParcelArrived(p.SenderName, p.Quick))
		}
	}
	wait := time.Until(p.ArrivesAt)
	if wait <= 0 {
		notify()
		return
	}
	time.AfterFunc(wait, notify)
}

func (s *Server) takeParcel(ctx context.Context, live *liveCharacter, c packetSink, dialog *economyDialog, id int64, remove bool) error {
	repo, ok := s.repo.(domain.ParcelRepository)
	if !ok {
		return c.WritePacket(protocol.ParcelResult(protocol.ParcelUnknownError))
	}
	dialog.sequence++
	req := domain.ParcelTake{OperationID: fmt.Sprintf("%s/%d", dialog.id, dialog.sequence), ParcelID: id, Delete: remove}
	live.persistMu.Lock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	result, err := repo.TakeParcel(ctx, req)
	live.persistMu.Unlock()
	if err != nil {
		return s.parcelFailure(live, c, err, parcelTakeCode(err))
	}
	if !remove {
		s.applyInventoryResult(live, result.Inventory.Character)
		if len(result.Inventory.Changes) > 0 {
			if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
				return err
			}
		}
		if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, true)); err != nil {
			return err
		}
	}
	return c.WritePacket(protocol.ParcelRemoved(id, remove))
}

// parcelFailure answers a refused request. A rule the player broke keeps the
// window open; stale ownership or an unknown outcome closes it, and the
// session reloads as storage does.
func (s *Server) parcelFailure(live *liveCharacter, c packetSink, err error, code byte) error {
	writeErr := c.WritePacket(protocol.ParcelResult(code))
	if code != protocol.ParcelUnknownError {
		return writeErr
	}
	s.closeEconomyDialog(live)
	if writeErr != nil {
		return writeErr
	}
	return err
}

func parcelSendCode(err error) byte {
	switch {
	case errors.Is(err, domain.ErrInsufficient):
		return protocol.ParcelNoMesos
	case errors.Is(err, domain.ErrParcelRecipient):
		return protocol.ParcelNoRecipient
	case errors.Is(err, domain.ErrParcelSameAccount):
		return protocol.ParcelSameAccount
	case errors.Is(err, domain.ErrParcelBoxFull):
		return protocol.ParcelBoxFull
	case errors.Is(err, domain.ErrParcelOnlyOne):
		return protocol.ParcelOnlyInBox
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		return protocol.ParcelBadRequest
	}
	return protocol.ParcelUnknownError
}

func parcelTakeCode(err error) byte {
	switch {
	case errors.Is(err, domain.ErrParcelNoRoom):
		return protocol.ParcelNoRoom
	case errors.Is(err, domain.ErrParcelOnlyOne):
		return protocol.ParcelOnlyHeld
	case errors.Is(err, domain.ErrParcelInTransit):
		// The window itself refuses (尚在運送中！); a request that gets here
		// anyway is not a proper one.
		return protocol.ParcelBadRequest
	case errors.Is(err, domain.ErrInsufficient):
		// Taking the mesos would pass the meso cap.
		return protocol.ParcelCannotTake
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		return protocol.ParcelBadRequest
	}
	return protocol.ParcelUnknownError
}
