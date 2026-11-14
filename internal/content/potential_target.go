package content

import (
	"fmt"

	"maple-go-server/internal/domain"
)

// PotentialTargetView keeps three distinct preimages. The stored value may
// omit native metadata; the original no-potential serializer then emits two
// unused0xffff option words and flags0xffff. Those unused words are not Java
// sealed-rank markers and must not be fed to the roll engine as negative IDs.
// Record this mapping under the same receipt as the eventual material/fee.
type PotentialTargetView struct {
	EquipmentBefore domain.Equipment          `json:"equipment_before"`
	NativeBefore    domain.EquipmentPotential `json:"native_before"`
	PolicyTarget    PotentialTarget           `json:"policy_target"`
	Normalization   string                    `json:"normalization"`
}

func PotentialOperationTarget(e domain.Equipment, item Item) (PotentialTargetView, error) {
	if e.ItemID != item.ID || item.InvType != 1 || e.ItemID < 1000000 || e.ItemID >= 2000000 ||
		e.Flags < 0 || e.Flags > 65535 || item.FixedPotentialOptionCount < 0 || item.FixedPotentialOptionCount > 5 {
		return PotentialTargetView{}, fmt.Errorf("invalid authoritative potential target")
	}
	native := e.Potential
	native.Present = true // Native serialization always has the fixed tail.
	normalization := "none"
	if e.Potential.IsZero() {
		native.Options = [5]uint16{0, 0, 0, 65535, 65535}
		native.Flags = 65535
		normalization = "absent_metadata_legacy_native_tail"
	}
	logical := native
	if native.RawState == 0 && native.Options[0] == 0 && native.Options[1] == 0 && native.Options[2] == 0 &&
		(native.Options[3] == 0 || native.Options[3] == 65535) &&
		(native.Options[4] == 0 || native.Options[4] == 65535) {
		if native.Options[3] != 0 || native.Options[4] != 0 {
			logical.Options[3], logical.Options[4] = 0, 0
			if normalization == "none" {
				normalization = "explicit_unrevealed_unused_sentinels"
			}
		}
	}
	// Exact nonzero/unknown payloads remain intact. The pure engine separately
	// rejects noncanonical rank/marker combinations before drawing or spending.
	target := PotentialTarget{ItemID: e.ItemID, RequiredLevel: item.ReqLevel, ItemFlags: uint16(e.Flags),
		Cash: item.Cash || e.CashID != 0, UpgradeSlots: e.UpgradeSlots, ScrollLevel: e.ScrollLevel, Potential: logical}
	for i := 0; i < item.FixedPotentialOptionCount; i++ {
		target.OptionLevelOverrides[i] = item.FixedPotentialOptions[i].Level
	}
	return PotentialTargetView{EquipmentBefore: e, NativeBefore: native, PolicyTarget: target, Normalization: normalization}, nil
}
