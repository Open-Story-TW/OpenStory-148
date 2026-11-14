package domain

import (
	"context"
	"errors"
)

// ErrAdminChanged: the equipment or stack is no longer what the operator was
// looking at when they made the edit.
var ErrAdminChanged = errors.New("item changed since it was read")

// The web admin's edits to a character's bags and balances. They are operator
// capabilities: never reachable from player chat, scripts or a game packet.
const (
	AdminSetEquipment = "set_equipment" // replace one equipment's stats
	AdminSetQuantity  = "set_quantity"  // change one stack's count
	AdminRemove       = "remove"        // delete one equipment or stack
	AdminGiveItem     = "give_item"     // add equipment or items by template
	AdminGiveMesos    = "give_mesos"    // add (or with a negative amount, take) mesos
)

// Wallet currencies, numbered as the cash ledger numbers them.
const (
	CashCurrencyCredit = 1 // Gash
	CashCurrencyPoints = 2 // Maple points
)

const (
	AdminEquipmentStatMax = 32767 // the client reads each stat as a signed short
	AdminEquipmentByteMax = 127
	AdminGiveMax          = 30000
)

type AdminInventoryRequest struct {
	OperationID string `json:"operation_id"`
	Actor       string `json:"actor"`
	CharacterID int64  `json:"character_id"`
	Kind        string `json:"kind"`
	// InvType and AssetID name the equipment (1) or stack (2..5) to change.
	InvType int   `json:"inv_type,omitempty"`
	AssetID int64 `json:"asset_id,omitempty"`
	// Equipment carries the new stats of set_equipment. Only the fourteen
	// stats, the remaining upgrade slots and the scroll count are read.
	Equipment *Equipment `json:"equipment,omitempty"`
	ItemID    int        `json:"item_id,omitempty"`
	Quantity  int        `json:"quantity,omitempty"`
	Mesos     int64      `json:"mesos,omitempty"`
	// Expect is what the operator saw of the equipment or stack being
	// changed. The edit is refused with ErrAdminChanged when the stored one
	// differs: the player used, scrolled or restacked it in the meantime.
	Expect *AdminExpect `json:"expect,omitempty"`
	// Session is set by the world that holds the character in memory. Without
	// it the character must be offline.
	Session *CharacterSession `json:"-"`
}

// AdminExpect is the operator's view of one item. Its position is left out:
// moving an item between slots does not change what an edit means.
type AdminExpect struct {
	ItemID   int `json:"item_id"`
	Quantity int `json:"quantity,omitempty"`
	// Equipment carries the same fields set_equipment writes.
	Equipment *Equipment `json:"equipment,omitempty"`
}

// MatchesEquipment reports whether e is still the equipment the operator saw.
func (x *AdminExpect) MatchesEquipment(e Equipment) bool {
	if x == nil {
		return true
	}
	if x.ItemID != e.ItemID || x.Equipment == nil {
		return false
	}
	s := *x.Equipment
	return s.STR == e.STR && s.DEX == e.DEX && s.INT == e.INT && s.LUK == e.LUK && s.HP == e.HP && s.MP == e.MP &&
		s.WATK == e.WATK && s.MATK == e.MATK && s.WDEF == e.WDEF && s.MDEF == e.MDEF && s.ACC == e.ACC && s.AVOID == e.AVOID &&
		s.Speed == e.Speed && s.Jump == e.Jump && s.UpgradeSlots == e.UpgradeSlots && s.ScrollLevel == e.ScrollLevel
}

// MatchesStack reports whether item is still the stack the operator saw.
func (x *AdminExpect) MatchesStack(item InventoryItem) bool {
	return x == nil || x.ItemID == item.TemplateID && x.Quantity == item.Quantity
}

type AdminInventoryResult struct {
	Character Character         `json:"character"`
	Changes   []InventoryChange `json:"changes"`
	// Worn reports that equipment the character is wearing changed, so its
	// stats and look must be refreshed.
	Worn     bool `json:"worn,omitempty"`
	Replayed bool `json:"replayed,omitempty"`
}

type AdminWalletRequest struct {
	OperationID string `json:"operation_id"`
	Actor       string `json:"actor"`
	AccountID   int64  `json:"account_id"`
	CharacterID int64  `json:"character_id"` // the character the admin was looking at; audit only
	Currency    int    `json:"currency"`
	Delta       int64  `json:"delta"`
}

type AdminInventoryRepository interface {
	ApplyAdminInventory(context.Context, AdminInventoryRequest) (AdminInventoryResult, error)
	AdjustAdminWallet(context.Context, AdminWalletRequest) (CashWallet, error)
	ReadAdminWallet(context.Context, int64) (CashWallet, error)
}
