package domain

import "context"

type StoredItem struct {
	ID        int64         `json:"id"`
	Position  int           `json:"position"`
	Item      InventoryItem `json:"item"`
	Equipment *Equipment    `json:"equipment,omitempty"`
}
type Storage struct {
	AccountID int64        `json:"account_id"`
	WorldID   int          `json:"world_id"`
	Capacity  int          `json:"capacity"`
	Mesos     int64        `json:"mesos"`
	Revision  int64        `json:"revision"`
	Items     []StoredItem `json:"items"`
}
type StorageRequest struct {
	Session          CharacterSession
	OperationID      string
	Kind             string
	Slot             int
	TemplateID       int
	Quantity         int
	StorageItemID    int64
	Delta            int64
	ExpectedRevision int64
}
type StorageResult struct {
	Inventory InventoryResult `json:"inventory"`
	Storage   Storage         `json:"storage"`
	Replayed  bool            `json:"replayed"`
}
type StorageRepository interface {
	OpenStorage(context.Context, CharacterSession) (Storage, error)
	ChangeStorage(context.Context, StorageRequest) (StorageResult, error)
}
