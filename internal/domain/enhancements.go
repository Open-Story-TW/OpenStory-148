package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

var ErrScrollOutcomeUnknown = errors.New("scroll outcome unknown")

type ScrollRequest struct {
	Session                            CharacterSession
	ClientTick                         uint32
	SourceSlot, TargetSlot, WhiteFlags int
	LegendarySpirit                    bool
}

type ScrollResult struct {
	Inventory                     InventoryResult
	Status                        int // 0 failure, 1 success, 2 curse/destruction.
	ScrollID, EquipmentTemplateID int
	LegendarySpirit, WhiteScroll  bool
	SuccessRoll, CurseRoll        int
	Replayed                      bool
	// New receipts retain an immutable, self-contained before/after plan.
	// Version-zero receipts still have Inventory.Character.Revision; a zero
	// CommittedRevision alone must never be interpreted as a fresh operation.
	CommittedRevision int64  `json:"committed_revision,omitempty"`
	PlanJSON          []byte `json:"plan_json,omitempty"`
	PlanSHA256        string `json:"plan_sha256,omitempty"`
}

type EnhancementRepository interface {
	ApplyScroll(context.Context, ScrollRequest) (ScrollResult, error)
}

// Live handlers require this stronger interface; ApplyScroll remains available
// to compatibility callers. Resolve never reapplies the operation or draws RNG.
type ScrollOutcomeRepository interface {
	EnhancementRepository
	ResolveScroll(context.Context, ScrollRequest) (ScrollResult, bool, error)
}

type ScrollOutcomePlan struct {
	Version                                        int
	SessionID                                      string
	Generation, CharacterID, AccountID             int64
	WorldID, ChannelID, MapID                      int
	ClientTick                                     uint32
	SourceSlot, TargetSlot, WhiteFlags             int
	LegendarySpirit                                bool
	BeforeRevision, CommittedRevision              int64
	MaterialBefore                                 InventoryItem
	WhiteBefore                                    *InventoryItem
	EquipmentBefore                                Equipment
	Stats                                          Equipment
	Success, Curse, SuccessRoll, CurseRoll, Status int
}

func EncodeScrollPlan(p ScrollOutcomePlan) ([]byte, string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:]), nil
}

func scrollAfter(p ScrollOutcomePlan) (Equipment, error) {
	e := p.EquipmentBefore
	if e.ID <= 0 || e.Slot != p.TargetSlot || e.UpgradeSlots < 1 || e.ScrollLevel >= 255 {
		return Equipment{}, ErrForbidden
	}
	if p.Status == 1 {
		pairs := []struct {
			to    *int
			delta int
		}{{&e.STR, p.Stats.STR}, {&e.DEX, p.Stats.DEX}, {&e.INT, p.Stats.INT}, {&e.LUK, p.Stats.LUK}, {&e.HP, p.Stats.HP}, {&e.MP, p.Stats.MP}, {&e.WATK, p.Stats.WATK}, {&e.MATK, p.Stats.MATK}, {&e.WDEF, p.Stats.WDEF}, {&e.MDEF, p.Stats.MDEF}, {&e.ACC, p.Stats.ACC}, {&e.AVOID, p.Stats.AVOID}, {&e.Speed, p.Stats.Speed}, {&e.Jump, p.Stats.Jump}}
		for _, v := range pairs {
			if *v.to < 0 || v.delta < 0 || *v.to > 32767-v.delta {
				return Equipment{}, ErrForbidden
			}
			*v.to += v.delta
		}
		e.ScrollLevel++
		e.UpgradeSlots--
	} else if p.WhiteFlags != 2 {
		e.UpgradeSlots--
	}
	return e, nil
}

// ValidateScrollReceipt checks the stored immutable result, before replay
// strips changes or substitutes a fresh aggregate. It does not read WZ or RNG.
func ValidateScrollReceipt(q ScrollRequest, v ScrollResult) error {
	c := v.Inventory.Character
	if c.ID != q.Session.CharacterID || c.AccountID != q.Session.AccountID || c.WorldID != q.Session.Lease.WorldID || c.Revision < 1 || v.Status < 0 || v.Status > 2 || v.SuccessRoll < 0 || v.SuccessRoll >= 10000 || v.CurseRoll < -1 || v.CurseRoll >= 10000 || v.LegendarySpirit != q.LegendarySpirit || v.WhiteScroll != (q.WhiteFlags == 2) {
		return ErrForbidden
	}
	var p ScrollOutcomePlan
	if len(v.PlanJSON) > 0 || v.PlanSHA256 != "" {
		h := sha256.Sum256(v.PlanJSON)
		if len(v.PlanJSON) == 0 || hex.EncodeToString(h[:]) != v.PlanSHA256 || json.Unmarshal(v.PlanJSON, &p) != nil {
			return ErrForbidden
		}
		if p.Version != 1 || p.SessionID != q.Session.SessionID || p.Generation != q.Session.Generation || p.CharacterID != q.Session.CharacterID || p.AccountID != q.Session.AccountID || p.WorldID != q.Session.Lease.WorldID || p.ChannelID != q.Session.ChannelID || p.ClientTick != q.ClientTick || p.SourceSlot != q.SourceSlot || p.TargetSlot != q.TargetSlot || p.WhiteFlags != q.WhiteFlags || p.LegendarySpirit != q.LegendarySpirit || p.CommittedRevision != v.CommittedRevision || p.CommittedRevision != c.Revision || p.BeforeRevision < 1 || p.BeforeRevision+1 != p.CommittedRevision || p.MapID != c.MapID || p.Success < 0 || p.Success > 100 || p.Curse < 0 || p.Curse > 100 || p.SuccessRoll != v.SuccessRoll || p.CurseRoll != v.CurseRoll || p.Status != v.Status {
			return ErrForbidden
		}
		status := 0
		if p.SuccessRoll < p.Success*100 {
			status = 1
		} else if p.Curse > 0 && p.CurseRoll >= 0 && p.CurseRoll < p.Curse*100 {
			status = 2
		}
		if status != p.Status || p.SuccessRoll < p.Success*100 && p.CurseRoll != -1 || p.SuccessRoll >= p.Success*100 && p.Curse > 0 && p.CurseRoll < 0 || p.Curse == 0 && p.CurseRoll != -1 {
			return ErrForbidden
		}
	} else {
		// Historical receipts have no detached plan, but retain their full
		// committed aggregate and delta. Accept only a structurally complete
		// canonical batch; never guess a missing committed revision.
		if v.CommittedRevision != 0 && v.CommittedRevision != c.Revision {
			return ErrForbidden
		}
		p = ScrollOutcomePlan{SourceSlot: q.SourceSlot, TargetSlot: q.TargetSlot, WhiteFlags: q.WhiteFlags, LegendarySpirit: q.LegendarySpirit, Status: v.Status}
		// Historical receipts carry their committed aggregate, not an immutable
		// before image. Reconstruct only quantity/identity proven by that
		// aggregate and canonical delta, never today's possibly reused slots.
		for _, i := range c.Items {
			if i.InvType == 2 && i.Slot == q.SourceSlot && i.TemplateID == v.ScrollID {
				p.MaterialBefore = i
				p.MaterialBefore.Quantity++
			}
		}
		if p.MaterialBefore.ID == 0 {
			// A legacy terminal remove has no durable item ID in its delta.
			// Preserve that absence instead of inventing one. A world fresh
			// delta must additionally bind the original pre-input snapshot.
			p.MaterialBefore = InventoryItem{InvType: 2, Slot: q.SourceSlot, TemplateID: v.ScrollID, Quantity: 1}
		}
		if q.WhiteFlags == 2 && len(v.Inventory.Changes) > 1 {
			// Only the recorded white delta slot can identify its remainder.
			// Another surviving white in a different slot is not that item.
			change := v.Inventory.Changes[1]
			p.WhiteBefore = nil
			for _, i := range c.Items {
				if i.InvType == 2 && i.Slot == change.Slot && i.TemplateID == 2340000 {
					copy := i
					copy.Quantity++
					p.WhiteBefore = &copy
				}
			}
			if p.WhiteBefore == nil && change.Mode == 3 && change.InvType == 2 {
				p.WhiteBefore = &InventoryItem{InvType: 2, Slot: change.Slot, TemplateID: 2340000, Quantity: 1}
			}
		}
		if v.Status != 2 {
			for _, e := range c.Equipment {
				if e.Slot == q.TargetSlot {
					p.EquipmentBefore = e
				}
			}
		}
	}
	if (len(v.PlanJSON) > 0 && (p.MaterialBefore.ID <= 0 || p.EquipmentBefore.ID <= 0)) || p.MaterialBefore.InvType != 2 || p.MaterialBefore.Slot != q.SourceSlot || p.MaterialBefore.TemplateID != v.ScrollID || p.MaterialBefore.Quantity < 1 || (p.EquipmentBefore.ID > 0 && (p.EquipmentBefore.Slot != q.TargetSlot || p.EquipmentBefore.ItemID != v.EquipmentTemplateID)) {
		return ErrForbidden
	}
	var changes []InventoryChange
	consume := func(i InventoryItem) error {
		left := i.Quantity - 1
		matches := 0
		for _, actual := range c.Items {
			if actual.ID == i.ID {
				matches++
				if left <= 0 || actual.Slot != i.Slot || actual.InvType != i.InvType || actual.TemplateID != i.TemplateID || actual.Quantity != left {
					return ErrForbidden
				}
			}
		}
		if matches > 1 || left > 0 && matches != 1 || left == 0 && matches != 0 {
			return ErrForbidden
		}
		change := InventoryChange{Mode: 3, InvType: 2, Slot: i.Slot}
		if left > 0 {
			change.Mode = 1
			change.Quantity = left
		}
		changes = append(changes, change)
		return nil
	}
	if err := consume(p.MaterialBefore); err != nil {
		return err
	}
	if q.WhiteFlags == 2 {
		if p.WhiteBefore == nil || p.WhiteBefore.TemplateID != 2340000 || p.WhiteBefore.InvType != 2 || p.WhiteBefore.Quantity < 1 || p.WhiteBefore.Slot < 1 || p.WhiteBefore.Slot > MaxInventorySlots || p.WhiteBefore.Slot == q.SourceSlot {
			return ErrForbidden
		}
		if err := consume(*p.WhiteBefore); err != nil {
			return err
		}
	}
	changes = append(changes, InventoryChange{Mode: 3, InvType: 1, Slot: q.TargetSlot})
	found := 0
	var actual Equipment
	for _, e := range c.Equipment {
		if p.EquipmentBefore.ID > 0 && e.ID == p.EquipmentBefore.ID || e.Slot == q.TargetSlot {
			found++
			actual = e
		}
	}
	if v.Status == 2 {
		if found != 0 {
			return ErrForbidden
		}
	} else {
		if found != 1 || actual.ID != p.EquipmentBefore.ID || actual.Slot != q.TargetSlot || actual.ItemID != v.EquipmentTemplateID {
			return ErrForbidden
		}
		if len(v.PlanJSON) > 0 {
			expected, err := scrollAfter(p)
			if err != nil || !reflect.DeepEqual(actual, expected) {
				return ErrForbidden
			}
		}
		changes = append(changes, InventoryChange{Mode: 0, InvType: 1, Slot: q.TargetSlot, Equipment: &actual})
	}
	if !reflect.DeepEqual(v.Inventory.Changes, changes) {
		return ErrForbidden
	}
	return nil
}
