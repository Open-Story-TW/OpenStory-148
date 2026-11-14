package content

import (
	"encoding/json"
	"reflect"
	"testing"

	"maple-go-server/internal/domain"
)

func potentialTargetFixture() (domain.Equipment, Item) {
	e := domain.Equipment{ID: 101, ItemID: 1302000, Slot: 1, UpgradeSlots: 7, Flags: 0x400, Enhance: 3}
	i := Item{ID: e.ItemID, InvType: 1, ReqLevel: 120, FixedPotentialOptionCount: 1,
		FixedPotentialOptions: [5]ItemPotentialOption{{ID: 60003, Level: 2}, {ID: 60002, Level: 17}}}
	return e, i
}

func TestPotentialOperationTargetSeparatesStoredNativeAndLogicalPreimages(t *testing.T) {
	e, item := potentialTargetFixture()
	before := e
	v, err := PotentialOperationTarget(e, item)
	if err != nil || v.EquipmentBefore != before || !v.EquipmentBefore.Potential.IsZero() ||
		v.NativeBefore.Options != [5]uint16{0, 0, 0, 65535, 65535} || v.NativeBefore.Flags != 65535 ||
		v.PolicyTarget.Potential.Options != [5]uint16{} || v.PolicyTarget.Potential.Flags != 65535 ||
		v.PolicyTarget.ItemFlags != 0x400 || v.PolicyTarget.OptionLevelOverrides != [5]int{2} ||
		v.Normalization != "absent_metadata_legacy_native_tail" {
		t.Fatal("absent state lost its actual native or policy preimage", v, err)
	}
	// Value copies must not mutate stored equipment or leak out-of-count WZ rows.
	v.PolicyTarget.Potential.Options[0] = 3
	if e != before || v.NativeBefore.Options[0] != 0 || v.PolicyTarget.OptionLevelOverrides[1] != 0 {
		t.Fatal("normalization mutated an input or adopted unknown template rows")
	}
}

func TestPotentialOperationTargetExplicitZeroOpaqueAndMarkersStayDistinct(t *testing.T) {
	e, item := potentialTargetFixture()
	for _, p := range []domain.EquipmentPotential{
		{Present: true},
		{Present: true, Options: [5]uint16{0, 0, 0, 65535, 0}, Flags: 0xa501},
		{Present: true, RawState: 1, Options: [5]uint16{65519, 65519}, Flags: 0x8085},
		{RawState: 0xf1, Options: [5]uint16{60003, 65535, 7, 8, 9}, Flags: 0x1f00},
	} {
		e.Potential = p
		v, err := PotentialOperationTarget(e, item)
		if err != nil || v.EquipmentBefore.Potential != p || v.NativeBefore.Options != p.Options || v.NativeBefore.Flags != p.Flags {
			t.Fatal("explicit native fields were replaced by legacy defaults", v, err)
		}
		if p.RawState != 0 && v.PolicyTarget.Potential.Options != p.Options {
			t.Fatal("sealed or unknown words were guessed into other option IDs")
		}
		data, err := json.Marshal(v)
		var replay PotentialTargetView
		if err != nil || json.Unmarshal(data, &replay) != nil || !reflect.DeepEqual(v, replay) {
			t.Fatal("receipt preimages do not roundtrip exactly", string(data), replay, err)
		}
	}
	// An explicitly observed all-zero tail must remain flags0, not0xffff.
	e.Potential = domain.EquipmentPotential{Present: true}
	v, _ := PotentialOperationTarget(e, item)
	if v.NativeBefore.Flags != 0 || v.PolicyTarget.Potential.Flags != 0 || v.Normalization != "none" {
		t.Fatal("explicit zero was mistaken for absence", v)
	}
}

func TestPotentialOperationTargetRejectsWrongAuthorityAndMarksCashIdentity(t *testing.T) {
	for which := 0; which < 4; which++ {
		e, item := potentialTargetFixture()
		switch which {
		case 0:
			item.ID++
		case 1:
			item.InvType = 2
		case 2:
			e.Flags = 65536
		case 3:
			item.FixedPotentialOptionCount = 6
		}
		if _, err := PotentialOperationTarget(e, item); err == nil {
			t.Fatal("invalid target authority accepted", which)
		}
	}
	e, item := potentialTargetFixture()
	e.CashID = 12345
	if v, err := PotentialOperationTarget(e, item); err != nil || !v.PolicyTarget.Cash {
		t.Fatal("cash serial was exposed as ordinary physical equipment", v, err)
	}
}
