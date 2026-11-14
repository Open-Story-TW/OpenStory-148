package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) handleScroll(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadScrollRequest(r)
	if err != nil {
		return err
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	reject := func() error { return s.writeScenePackets(visit, protocol.EnableActions()) }
	live.mu.Lock()
	req.Session = cloneCombatSession(live.session)
	live.mu.Unlock()
	req.Session.Lease = s.currentLease()
	if err := assetOutcomeUncertain(live, req.Session); err != nil {
		return err
	}
	if s.gmBusy(live) {
		return reject()
	}
	repo, ok := s.repo.(domain.ScrollOutcomeRepository)
	if !ok {
		return reject()
	} // no unsafe Apply-only fallback
	if s.combatVitalsPending(live, req.Session) {
		return reject()
	}
	if err := s.writeScenePackets(visit); err != nil {
		return err
	}
	result, err := repo.ApplyScroll(ctx, req)
	resolved := false
	receiptObserved := errors.Is(err, domain.ErrScrollOutcomeUnknown) || result.Replayed
	resolve := func() error {
		live.scrollUncertain = &req
		check, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var found bool
		result, found, err = repo.ResolveScroll(check, req)
		if err != nil {
			return fmt.Errorf("%w: %v", domain.ErrScrollOutcomeUnknown, err)
		}
		if !found {
			if receiptObserved {
				return fmt.Errorf("%w: previously observed receipt absent", domain.ErrScrollOutcomeUnknown)
			}
			live.scrollUncertain = nil
			return nil
		}
		resolved = true
		return nil
	}
	if err != nil && !scrollKnownRollback(err) {
		if e := resolve(); e != nil {
			return e
		}
		if !resolved {
			return reject()
		}
	}
	if err != nil {
		slog.Debug("scroll_rejected", "character", req.Session.CharacterID, "error", err)
		if errors.Is(err, domain.ErrStaleOwner) {
			return err
		}
		return reject()
	}
	if result.CommittedRevision == 0 && len(result.PlanJSON) == 0 && result.PlanSHA256 == "" {
		result.CommittedRevision = result.Inventory.Character.Revision
	}
	if result.Replayed && !resolved && result.CommittedRevision > req.Session.Character.Revision {
		// The old operation committed but this live aggregate has not seen it.
		// Read its original exact-revision delta; never call Apply/RNG again.
		if e := resolve(); e != nil {
			return e
		}
		if !resolved {
			return fmt.Errorf("%w: replay receipt disappeared", domain.ErrScrollOutcomeUnknown)
		}
	}
	live.scrollUncertain = &req
	delta := !result.Replayed || resolved && result.CommittedRevision > req.Session.Character.Revision
	if err = validateWorldScroll(req, result, delta); err != nil {
		return err
	}
	var packets [][]byte
	var effect []byte
	if delta {
		packet := protocol.ModifyInventory(result.Inventory.Changes)
		if packet == nil {
			return domain.ErrScrollOutcomeUnknown
		}
		packets = append(packets, packet)
		before, after := req.Session.Character, result.Inventory.Character
		if req.TargetSlot < 0 || before.HP != after.HP || before.MP != after.MP || before.MaxHP != after.MaxHP || before.MaxMP != after.MaxMP {
			packets = append(packets, protocol.InventoryVitals(after))
		}
		effect = protocol.ScrollEffect(req.Session.CharacterID, result)
		if effect == nil {
			return domain.ErrScrollOutcomeUnknown
		}
		packets = append(packets, effect)
	}
	packets = append(packets, protocol.EnableActions())
	unlock, err := s.lockScenePublication(req.Session.CharacterID)
	if err != nil {
		return err
	}
	if !s.sceneCurrent(visit) {
		unlock()
		return domain.ErrStaleOwner
	}
	live.mu.Lock()
	revision := live.session.Character.Revision
	live.mu.Unlock()
	if revision != req.Session.Character.Revision {
		unlock()
		return domain.ErrStaleOwner
	}
	if delta {
		s.applyInventoryResult(live, result.Inventory.Character)
	}
	live.scrollUncertain = nil
	if delta && req.TargetSlot < 0 {
		s.refreshCharacterLookScene(visit)
	}
	for _, p := range packets {
		if err = sink.WritePacket(p); err != nil {
			unlock()
			return err
		}
	}
	unlock()
	if delta {
		slog.Info("scroll_command_committed", "character", req.Session.CharacterID, "client_tick", req.ClientTick, "status", result.Status, "revision", result.CommittedRevision, "same_operation_resolved", resolved)
		s.broadcastPotentialEffect(visit, effect) // shared exact-peer-visit publication; no nested CID gates
	}
	return nil
}

func scrollKnownRollback(err error) bool {
	if errors.Is(err, domain.ErrScrollOutcomeUnknown) {
		return false
	}
	return errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrStaleOwner)
}

// persistMu protects the pointer; only this exact owner is fenced. A replacement
// generation sharing the live pointer is not held hostage by the old request.
func scrollUncertainFor(live *liveCharacter, s domain.CharacterSession) bool {
	q := live.scrollUncertain
	return q != nil && q.Session.CharacterID == s.CharacterID && q.Session.AccountID == s.AccountID && q.Session.SessionID == s.SessionID && q.Session.Generation == s.Generation && q.Session.Lease.WorldID == s.Lease.WorldID && q.Session.Lease.InstanceID == s.Lease.InstanceID && q.Session.Lease.Epoch == s.Lease.Epoch
}

func assetOutcomeUncertain(live *liveCharacter, s domain.CharacterSession) error {
	if scrollUncertainFor(live, s) {
		return domain.ErrScrollOutcomeUnknown
	}
	if itemUseUncertainFor(live, s) {
		return domain.ErrItemUseOutcomeUnknown
	}
	return nil
}

func validateWorldScroll(q domain.ScrollRequest, v domain.ScrollResult, delta bool) error {
	c, b := v.Inventory.Character, q.Session.Character
	if c.ID != q.Session.CharacterID || c.AccountID != q.Session.AccountID || c.WorldID != q.Session.Lease.WorldID || c.MapID != b.MapID || v.CommittedRevision < 1 || v.CommittedRevision > c.Revision || v.LegendarySpirit != q.LegendarySpirit || v.WhiteScroll != (q.WhiteFlags == 2) {
		return domain.ErrScrollOutcomeUnknown
	}
	if !delta {
		if !v.Replayed || len(v.Inventory.Changes) != 0 || v.CommittedRevision > b.Revision || c.Revision != b.Revision {
			return domain.ErrScrollOutcomeUnknown
		}
		if len(v.PlanJSON) > 0 || v.PlanSHA256 != "" {
			h := sha256.Sum256(v.PlanJSON)
			var p domain.ScrollOutcomePlan
			if hex.EncodeToString(h[:]) != v.PlanSHA256 || json.Unmarshal(v.PlanJSON, &p) != nil || p.SessionID != q.Session.SessionID || p.Generation != q.Session.Generation || p.ClientTick != q.ClientTick || p.CommittedRevision != v.CommittedRevision {
				return domain.ErrScrollOutcomeUnknown
			}
		}
		return nil // no old snapshot merge, HP correction, inventory or effect replay
	}
	if c.Revision != b.Revision+1 || v.CommittedRevision != c.Revision || domain.ValidateScrollReceipt(q, v) != nil {
		return domain.ErrScrollOutcomeUnknown
	}
	if c.MaxHP < 1 || c.MaxMP < 0 || c.HP != min(b.HP, c.MaxHP) || c.MP != min(b.MP, c.MaxMP) || c.BaseMaxHP != b.BaseMaxHP || c.BaseMaxMP != b.BaseMaxMP || c.VitalsModel != b.VitalsModel || c.VitalsOrigin != b.VitalsOrigin {
		return domain.ErrScrollOutcomeUnknown
	}
	var material domain.InventoryItem
	var equipment domain.Equipment
	var white *domain.InventoryItem
	for _, i := range b.Items {
		if i.InvType == 2 && i.Slot == q.SourceSlot {
			material = i
		}
	}
	for _, e := range b.Equipment {
		if e.Slot == q.TargetSlot {
			equipment = e
		}
	}
	if material.ID <= 0 || equipment.ID <= 0 || material.TemplateID != v.ScrollID || equipment.ItemID != v.EquipmentTemplateID {
		return domain.ErrScrollOutcomeUnknown
	}
	if len(v.PlanJSON) > 0 {
		var p domain.ScrollOutcomePlan
		if json.Unmarshal(v.PlanJSON, &p) != nil || !reflect.DeepEqual(p.MaterialBefore, material) || !reflect.DeepEqual(p.EquipmentBefore, equipment) {
			return domain.ErrScrollOutcomeUnknown
		}
		if q.WhiteFlags == 2 {
			// The SQL aggregate is slot-sorted, while a live MoveInventory or
			// grant can leave its slice unsorted. Bind the actual committed
			// white-scroll identity, never the first live slice element.
			if p.WhiteBefore == nil {
				return domain.ErrScrollOutcomeUnknown
			}
			for _, i := range b.Items {
				if i.ID == p.WhiteBefore.ID && reflect.DeepEqual(i, *p.WhiteBefore) && i.InvType == 2 && i.TemplateID == 2340000 && i.Quantity > 0 {
					copy := i
					white = &copy
				}
			}
		}
	} else if q.WhiteFlags == 2 {
		// Legacy deltas prove the consumed slot even when its terminal item
		// is absent from the committed snapshot and another white remains.
		slot := v.Inventory.Changes[1].Slot // canonical batch validated above
		for _, i := range b.Items {
			if i.InvType == 2 && i.Slot == slot && i.TemplateID == 2340000 && i.Quantity > 0 {
				copy := i
				white = &copy
			}
		}
	}
	items := append([]domain.InventoryItem(nil), b.Items...)
	consume := func(id int64) bool {
		for i := range items {
			if items[i].ID == id {
				items[i].Quantity--
				items[i].Revision++
				if items[i].Quantity == 0 {
					items = append(items[:i], items[i+1:]...)
				}
				return true
			}
		}
		return false
	}
	if !consume(material.ID) || q.WhiteFlags == 2 && (white == nil || !consume(white.ID)) {
		return domain.ErrScrollOutcomeUnknown
	}
	if !sameScrollItems(items, c.Items) {
		return domain.ErrScrollOutcomeUnknown
	}
	var expected []domain.Equipment
	for _, e := range b.Equipment {
		if e.ID != equipment.ID {
			expected = append(expected, e)
		}
	}
	if v.Status != 2 {
		for _, e := range c.Equipment {
			if e.ID == equipment.ID {
				expected = append(expected, e)
			}
		}
	}
	if len(expected) != len(c.Equipment) {
		return domain.ErrScrollOutcomeUnknown
	}
	for _, e := range expected {
		found := false
		for _, actual := range c.Equipment {
			if actual.ID == e.ID && reflect.DeepEqual(actual, e) {
				found = true
				break
			}
		}
		if !found {
			return domain.ErrScrollOutcomeUnknown
		}
	}
	if c.Mesos != b.Mesos || !reflect.DeepEqual(c.InventorySlots, b.InventorySlots) {
		return domain.ErrScrollOutcomeUnknown
	}
	return nil
}

func sameScrollItems(a, b []domain.InventoryItem) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int64]bool{}
	for _, i := range a {
		found := false
		for _, j := range b {
			if j.ID == i.ID && reflect.DeepEqual(i, j) && !seen[j.ID] {
				seen[j.ID] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
