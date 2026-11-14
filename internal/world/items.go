package world

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// handleItemUse is the only live item path. There is deliberately no fallback
// to Repository.UseItem: that API cannot resolve an uncertain commit.
func (s *Server) handleItemUse(ctx context.Context, live *liveCharacter, sink packetSink, q domain.UseItemRequest) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	reject := func() error { return s.writeScenePackets(visit, protocol.EnableActions()) }
	if s.gmBusy(live) {
		return reject()
	}
	repo, ok := s.repo.(domain.ItemUseRepository)
	if !ok {
		return fmt.Errorf("%w: item use Apply/Resolve unavailable", domain.ErrItemUseOutcomeUnknown)
	}
	live.mu.Lock()
	q.Session = cloneCombatSession(live.session)
	live.mu.Unlock()
	q.Session.Lease = s.currentLease()
	if err := assetOutcomeUncertain(live, q.Session); err != nil {
		return err
	}
	if s.combatVitalsPending(live, q.Session) {
		return reject()
	}
	if err := s.writeScenePackets(visit); err != nil {
		return err
	}
	result, err := repo.ApplyItemUse(ctx, q)
	resolved := false
	if err != nil && !itemUseKnownRollback(err) {
		live.itemUseUncertain = &q
		check, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var found bool
		result, found, err = repo.ResolveItemUse(check, q)
		cancel()
		if err == nil && !found {
			live.itemUseUncertain = nil
			return reject()
		}
		if err != nil {
			return fmt.Errorf("%w: %v", domain.ErrItemUseOutcomeUnknown, err)
		}
		resolved = true
	}
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return err
		}
		return reject()
	}
	live.itemUseUncertain = &q
	delta := !result.Replayed
	if resolved {
		delta = result.CommittedRevision > q.Session.Character.Revision
	}
	if err := validateWorldItemUse(q, result, delta); err != nil {
		return err
	}
	unlock, err := s.lockScenePublication(q.Session.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(visit) {
		return domain.ErrStaleOwner
	}
	// The persistMu excludes another durable operation, but scene replacement
	// and gameplay vitals can still change while SQL is in flight. Only this
	// exact input revision is authorized to receive the captured new delta.
	live.mu.Lock()
	current := live.session
	live.mu.Unlock()
	if current.Character.Revision != q.Session.Character.Revision {
		return domain.ErrStaleOwner
	}
	if !delta {
		live.itemUseUncertain = nil
		return sink.WritePacket(protocol.EnableActions())
	}
	s.applyInventoryResult(live, result.Character)
	live.itemUseUncertain = nil
	if err = sink.WritePacket(protocol.UpdateUseSlot(q.Slot, result.QuantityAfter)); err != nil {
		return err
	}
	s.applyItemBuff(live, sink, result)
	// Always publish HP and MP, including a maximum-buff expiry clamp on an
	// HP-only red potion. This is one ordered batch under this scene gate.
	return sink.WritePacket(protocol.InventoryVitals(result.Character))
}

// Caller owns persistMu. It cannot block a replacement generation, even if a
// test or a reconnect reuses this live pointer. A fatal unknown retains this
// gate until the old scene is detached; only read-only Resolve can clear it.
func itemUseUncertainFor(live *liveCharacter, session domain.CharacterSession) bool {
	q := live.itemUseUncertain
	return q != nil && q.Session.CharacterID == session.CharacterID && q.Session.AccountID == session.AccountID &&
		q.Session.SessionID == session.SessionID && q.Session.Generation == session.Generation && q.Session.Lease.WorldID == session.Lease.WorldID &&
		q.Session.Lease.InstanceID == session.Lease.InstanceID && q.Session.Lease.Epoch == session.Lease.Epoch
}

func itemUseKnownRollback(err error) bool {
	return errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrStaleOwner)
}

func validateWorldItemUse(q domain.UseItemRequest, r domain.ItemUseResult, delta bool) error {
	c, before := r.Character, q.Session.Character
	if c.ID != q.Session.CharacterID || c.AccountID != q.Session.AccountID || c.WorldID != q.Session.Lease.WorldID || c.MapID != before.MapID ||
		r.SessionID != q.Session.SessionID || r.Generation != q.Session.Generation || r.ChannelID != q.Session.ChannelID || r.ClientTick != q.ClientTick || r.Slot != q.Slot || r.TemplateID != q.TemplateID ||
		r.ItemInstanceID <= 0 || r.CommittedRevision <= 0 || r.CommittedRevision > c.Revision || c.Revision < before.Revision {
		return fmt.Errorf("%w: item receipt owner/key", domain.ErrItemUseOutcomeUnknown)
	}
	if !delta {
		if r.CommittedRevision > before.Revision || c.Revision != before.Revision || !sameInventoryItems(c.Items, before.Items) {
			return domain.ErrStaleOwner
		}
		return nil // Never install a replay's checkpointed HP/MP.
	}
	if r.Replayed || r.CommittedRevision != before.Revision+1 || c.Revision != r.CommittedRevision || r.HPBefore != before.HP || r.MPBefore != before.MP {
		return fmt.Errorf("%w: item receipt revision/vitals", domain.ErrItemUseOutcomeUnknown)
	}
	expected := append([]domain.InventoryItem(nil), before.Items...)
	found := false
	for i, item := range expected {
		if item.InvType != 2 || item.Slot != q.Slot {
			continue
		}
		if found || item.ID != r.ItemInstanceID || item.TemplateID != q.TemplateID || item.Quantity < 1 || r.QuantityAfter != item.Quantity-1 {
			return domain.ErrItemUseOutcomeUnknown
		}
		found = true
		if r.QuantityAfter == 0 {
			expected = append(expected[:i], expected[i+1:]...)
		} else {
			expected[i].Quantity--
			expected[i].Revision++
		}
		break
	}
	if !found || !sameInventoryItems(expected, c.Items) {
		return domain.ErrItemUseOutcomeUnknown
	}
	v, err := content.CalculatePotionRecovery(content.Recovery{HP: r.FlatHP, MP: r.FlatMP, HPRate: r.HPRate, MPRate: r.MPRate}, content.PotionVitals{HP: before.HP, MP: before.MP, MaxHP: c.MaxHP, MaxMP: c.MaxMP}, content.PotionRecoveryModifiers{FixedHPPercent: r.FixedHPPercent}, domain.IsDemonJob(c.Job))
	if err != nil || v.HP != c.HP || v.MP != c.MP {
		return domain.ErrItemUseOutcomeUnknown
	}
	return nil
}

// sameInventoryItems compares two bags as sets of rows. The live bag keeps the
// order items were gained in (a new stack is appended), while a committed
// character is read back ordered by inventory and slot, so the same bag can
// arrive in two orders.
func sameInventoryItems(a, b []domain.InventoryItem) bool {
	if len(a) != len(b) {
		return false
	}
	order := func(x, y domain.InventoryItem) int {
		return cmp.Or(cmp.Compare(x.InvType, y.InvType), cmp.Compare(x.Slot, y.Slot), cmp.Compare(x.ID, y.ID))
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, order)
	slices.SortFunc(b, order)
	return slices.Equal(a, b)
}

// Compatibility helper for older internal callers/tests. Network dispatch is
// exclusively handleItemUse above, which never enters this unsafe API shape.
func (s *Server) useItem(ctx context.Context, live *liveCharacter, req domain.UseItemRequest) (domain.Character, error) {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	updated, err := s.repo.UseItem(ctx, req)
	if err != nil {
		return domain.Character{}, err
	}
	live.mu.Lock()
	// A move may arrive while SQL is running. Retain its location/dirty flag;
	// replace only the fields committed by this command.
	live.session.Character.Items = updated.Items
	if updated.Revision > live.session.Character.Revision {
		live.session.Character.HP = updated.HP
		live.session.Character.MP = updated.MP
	}
	updated.HP = live.session.Character.HP
	updated.MP = live.session.Character.MP
	live.session.Character.Revision = updated.Revision
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	s.refreshParty(live)
	return updated, nil
}

func (s *Server) adjustMesos(ctx context.Context, live *liveCharacter, delta int64, operationID, reason string) (domain.Character, error) {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	updated, err := s.repo.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: delta, OperationID: operationID, Reason: reason})
	if err != nil {
		return domain.Character{}, err
	}
	live.mu.Lock()
	live.session.Character.Mesos = updated.Mesos
	live.session.Character.Revision = updated.Revision
	live.mu.Unlock()
	return updated, nil
}
