package domain

import "context"

// InventoryMoveRequest is the native v145 ITEM_MOVE command. Negative slots
// belong to worn equipment; destination zero means a drop on the current map.
type InventoryMoveRequest struct {
	Session     CharacterSession
	ClientTick  uint32
	InvType     int
	Source      int
	Destination int
	Quantity    int
}

type InventoryChange struct {
	Mode        int            `json:"mode"`
	InvType     int            `json:"inv_type"`
	Slot        int            `json:"slot"`
	Destination int            `json:"destination,omitempty"`
	Quantity    int            `json:"quantity,omitempty"`
	Equipment   *Equipment     `json:"equipment,omitempty"`
	Item        *InventoryItem `json:"item,omitempty"`
}

type ItemDrop struct {
	ObjectID        int32      `json:"object_id"`
	WorldID         int        `json:"world_id"`
	ChannelID       int        `json:"channel_id"`
	MapID           int        `json:"map_id"`
	OwnerID         int64      `json:"owner_id"`
	X               int        `json:"x"`
	Y               int        `json:"y"`
	InvType         int        `json:"inv_type"`
	TemplateID      int        `json:"template_id"`
	Quantity        int        `json:"quantity"`
	Mesos           int64      `json:"mesos,omitempty"`
	PlayerDrop      bool       `json:"player_drop,omitempty"`
	QuestID         int        `json:"quest_id,omitempty"`
	QuestDropType   int        `json:"quest_drop_type,omitempty"`
	QuestPartyID    int64      `json:"quest_party_id,omitempty"`
	QuestOwnerUntil int64      `json:"quest_owner_until,omitempty"` // Unix milliseconds; 0 means FFA.
	Equipment       *Equipment `json:"equipment,omitempty"`
}

type InventoryResult struct {
	Character Character         `json:"character"`
	Changes   []InventoryChange `json:"changes"`
	Drop      *ItemDrop         `json:"drop,omitempty"`
	Replayed  bool              `json:"replayed"`
}

type ItemPickupRequest struct {
	Session  CharacterSession
	ObjectID int32
}

// InventoryRepository is separate while the inventory track is integrated.
type InventoryRepository interface {
	MoveInventory(context.Context, InventoryMoveRequest) (InventoryResult, error)
	PickupItem(context.Context, ItemPickupRequest) (InventoryResult, error)
	ListItemDrops(context.Context, int, int, int) ([]ItemDrop, error)
}
