package domain

import (
	"context"
	"errors"
)

var ErrItemUseOutcomeUnknown = errors.New("item use outcome unknown; reconnect required")
var ErrLegacyItemUseReceipt = errors.New("legacy item use receipt cannot prove full result")
var ErrItemUseReceiptInvalid = errors.New("item use receipt is invalid")

// ItemUseResult captures the committed aggregate. Resolve never recalculates
// recovery or spends another item. A normal duplicate Apply returns a fresh
// aggregate with Replayed=true; it must not reinstall an older HP delta.
type ItemUseResult struct {
	Character         Character `json:"character"`
	SessionID         string    `json:"session_id"`
	Generation        int64     `json:"generation"`
	ChannelID         int       `json:"channel_id"`
	ClientTick        uint32    `json:"client_tick"`
	Slot              int       `json:"slot"`
	TemplateID        int       `json:"template_id"`
	ItemInstanceID    int64     `json:"item_instance_id"`
	QuantityAfter     int       `json:"quantity_after"`
	CommittedRevision int64     `json:"committed_revision"`
	HPBefore          int       `json:"hp_before"`
	MPBefore          int       `json:"mp_before"`
	FlatHP            int       `json:"flat_hp"`
	FlatMP            int       `json:"flat_mp"`
	HPRate            int       `json:"hp_rate"`
	MPRate            int       `json:"mp_rate"`
	FixedHPPercent    int       `json:"fixed_hp_percent"`
	// Buffs are the rows a timed item effect added; Replaced are the rows
	// they took the stat of.
	Buffs    []SkillBuff `json:"buffs,omitempty"`
	Replaced []SkillBuff `json:"replaced,omitempty"`
	Replayed bool        `json:"replayed,omitempty"`
}

type ItemUseRepository interface {
	ApplyItemUse(context.Context, UseItemRequest) (ItemUseResult, error)
	ResolveItemUse(context.Context, UseItemRequest) (ItemUseResult, bool, error)
}
