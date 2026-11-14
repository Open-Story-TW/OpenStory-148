package domain

import "context"

type ShopItem struct {
	ID               int   `json:"id"`
	TemplateID       int   `json:"template_id"`
	Price            int64 `json:"price"`
	Position         int   `json:"position"`
	RequiredItem     int   `json:"required_item"`
	RequiredQuantity int   `json:"required_quantity"`
	PointType        int   `json:"point_type"`
	Period           int   `json:"period"`
	State            int   `json:"state"`
	Category         int   `json:"category"`
	MinLevel         int   `json:"min_level"`
}
type ShopRank struct {
	ItemID int    `json:"item_id"`
	Name   string `json:"name"`
}
type Shop struct {
	ID         int        `json:"id"`
	NPCID      int        `json:"npc_id"`
	Name       string     `json:"name"`
	Items      []ShopItem `json:"items"`
	Ranks      []ShopRank `json:"ranks"`
	SourceHash string     `json:"source_hash"`
}
type ShopRequest struct {
	Session     CharacterSession
	OperationID string
	ShopID      int
	Kind        string
	EntryID     int
	TemplateID  int
	Slot        int
	Quantity    int
	// Optional native UI assertion; nil preserves internal callers and old receipts.
	DisplayedUnitPrice *int64
}
type ShopResult struct {
	Inventory InventoryResult `json:"inventory"`
	Replayed  bool            `json:"replayed"`
}
type ShopRepository interface {
	ChangeShop(context.Context, ShopRequest) (ShopResult, error)
}
