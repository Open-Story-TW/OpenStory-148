package domain

import (
	"encoding/json"
	"testing"
)

func scrollReceiptFixture(t *testing.T, status int, white, terminal bool) (ScrollRequest, ScrollResult) {
	t.Helper()
	b := Character{ID: 1, AccountID: 2, WorldID: 0, MapID: 100, Revision: 4, Mesos: 123, HP: 80, MP: 20, MaxHP: 100, MaxMP: 30}
	eq := Equipment{ID: 10, ItemID: 1322005, Slot: -11, WATK: 19, UpgradeSlots: 7, Enhance: 3, Potential: EquipmentPotential{Present: true, RawState: 18, Options: [5]uint16{20353, 10014}, Flags: 65535}}
	qty := 2
	if terminal {
		qty = 1
	}
	mat := InventoryItem{ID: 20, TemplateID: 2043200, InvType: 2, Slot: 2, Quantity: qty, Revision: 3}
	b.Equipment = []Equipment{eq}
	b.Items = []InventoryItem{mat}
	q := ScrollRequest{Session: CharacterSession{CharacterID: 1, AccountID: 2, SessionID: "receipt-session", Generation: 3, ChannelID: 5, Lease: WorldLease{WorldID: 0}, Character: b}, ClientTick: 99, SourceSlot: 2, TargetSlot: -11}
	// Use a JSON copy so after-mutations cannot rewrite the bound before image.
	raw, _ := json.Marshal(b)
	var c Character
	_ = json.Unmarshal(raw, &c)
	c.Revision++
	p := ScrollOutcomePlan{Version: 1, SessionID: q.Session.SessionID, Generation: 3, CharacterID: 1, AccountID: 2, WorldID: 0, ChannelID: 5, MapID: 100, ClientTick: 99, SourceSlot: 2, TargetSlot: -11, BeforeRevision: 4, CommittedRevision: 5, MaterialBefore: mat, EquipmentBefore: eq, Stats: Equipment{WATK: 2}, Success: 60, Curse: 20, SuccessRoll: 7000, CurseRoll: 3000, Status: status}
	if status == 1 {
		p.SuccessRoll = 100
		p.CurseRoll = -1
	}
	if status == 2 {
		p.CurseRoll = 100
	}
	v := ScrollResult{Status: status, ScrollID: 2043200, EquipmentTemplateID: 1322005, SuccessRoll: p.SuccessRoll, CurseRoll: p.CurseRoll, CommittedRevision: 5}
	if terminal {
		c.Items = nil
		v.Inventory.Changes = append(v.Inventory.Changes, InventoryChange{Mode: 3, InvType: 2, Slot: 2})
	} else {
		c.Items[0].Quantity--
		c.Items[0].Revision++
		v.Inventory.Changes = append(v.Inventory.Changes, InventoryChange{Mode: 1, InvType: 2, Slot: 2, Quantity: 1})
	}
	if white {
		w := InventoryItem{ID: 21, TemplateID: 2340000, InvType: 2, Slot: 6, Quantity: 1}
		q.Session.Character.Items = append(q.Session.Character.Items, w)
		p.WhiteBefore = &w
		p.WhiteFlags = 2
		q.WhiteFlags = 2
		v.WhiteScroll = true
		v.Inventory.Changes = append(v.Inventory.Changes, InventoryChange{Mode: 3, InvType: 2, Slot: 6})
	}
	v.Inventory.Changes = append(v.Inventory.Changes, InventoryChange{Mode: 3, InvType: 1, Slot: -11})
	if status == 2 {
		c.Equipment = nil
	} else {
		after, e := scrollAfter(p)
		if e != nil {
			t.Fatal(e)
		}
		c.Equipment[0] = after
		v.Inventory.Changes = append(v.Inventory.Changes, InventoryChange{Mode: 0, InvType: 1, Slot: -11, Equipment: &c.Equipment[0]})
	}
	v.Inventory.Character = c
	v.PlanJSON, v.PlanSHA256, _ = EncodeScrollPlan(p)
	return q, v
}

func TestScrollReceiptImmutableCanonicalOutcomes(t *testing.T) {
	for _, status := range []int{0, 1, 2} {
		for _, white := range []bool{false, true} {
			for _, terminal := range []bool{false, true} {
				q, v := scrollReceiptFixture(t, status, white, terminal)
				if err := ValidateScrollReceipt(q, v); err != nil {
					t.Fatal(status, white, terminal, err)
				}
				raw, _ := json.Marshal(v)
				var round ScrollResult
				_ = json.Unmarshal(raw, &round)
				if err := ValidateScrollReceipt(q, round); err != nil {
					t.Fatal("JSON receipt", err)
				}
				v.PlanJSON = nil
				v.PlanSHA256 = ""
				v.CommittedRevision = 0
				if err := ValidateScrollReceipt(q, v); err != nil {
					t.Fatal("legacy original snapshot", status, white, terminal, err)
				}
				q.Session.Character = v.Inventory.Character
				if err := ValidateScrollReceipt(q, v); err != nil {
					t.Fatal("legacy post-commit snapshot", status, white, terminal, err)
				}
			}
		}
	}
}
func TestScrollReceiptRejectsForgeryAndRetainsOpaqueEquipment(t *testing.T) {
	for _, mutate := range []struct {
		name string
		f    func(*ScrollRequest, *ScrollResult)
	}{
		{"hash", func(_ *ScrollRequest, v *ScrollResult) { v.PlanSHA256 = "bad" }},
		{"wrong key", func(q *ScrollRequest, _ *ScrollResult) { q.ClientTick++ }},
		{"wrong owner", func(q *ScrollRequest, _ *ScrollResult) { q.Session.Generation++ }},
		{"quantity", func(_ *ScrollRequest, v *ScrollResult) { v.Inventory.Changes[0].Quantity = 99 }},
		{"equipment delta", func(_ *ScrollRequest, v *ScrollResult) { v.Inventory.Changes[2].Equipment.WATK++ }},
		{"potential", func(_ *ScrollRequest, v *ScrollResult) {
			v.Inventory.Character.Equipment[0].Potential.Options[4] = 60003
		}},
		{"enhance", func(_ *ScrollRequest, v *ScrollResult) { v.Inventory.Character.Equipment[0].Enhance++ }},
		{"future revision", func(_ *ScrollRequest, v *ScrollResult) { v.Inventory.Character.Revision++ }},
		{"status", func(_ *ScrollRequest, v *ScrollResult) { v.Status = 2 }},
		{"no legacy revision", func(_ *ScrollRequest, v *ScrollResult) {
			v.PlanJSON = nil
			v.PlanSHA256 = ""
			v.CommittedRevision = 0
			v.Inventory.Character.Revision = 0
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			q, v := scrollReceiptFixture(t, 1, false, false)
			mutate.f(&q, &v)
			if ValidateScrollReceipt(q, v) == nil {
				t.Fatal("forgery accepted")
			}
		})
	}
}

func TestScrollReceiptLegacyTerminalWhiteDoesNotConsumeOtherRemainingWhite(t *testing.T) {
	q, v := scrollReceiptFixture(t, 1, true, true)
	other := InventoryItem{ID: 88, TemplateID: 2340000, InvType: 2, Slot: 10, Quantity: 4, Revision: 9}
	// Legacy committed snapshot has no original white slot6, but still has
	// a different white at slot10. The input snapshot can now be post-commit.
	v.Inventory.Character.Items = append(v.Inventory.Character.Items, other)
	v.PlanJSON = nil
	v.PlanSHA256 = ""
	v.CommittedRevision = 0
	q.Session.Character = v.Inventory.Character
	if err := ValidateScrollReceipt(q, v); err != nil {
		t.Fatal("legacy remaining other white mistaken for consumed slot", err)
	}
	// A forged nonterminal delta for the consumed slot cannot borrow slot10.
	v.Inventory.Changes[1] = InventoryChange{Mode: 1, InvType: 2, Slot: 6, Quantity: 3}
	if ValidateScrollReceipt(q, v) == nil {
		t.Fatal("different remaining white satisfied forged delta")
	}
}
