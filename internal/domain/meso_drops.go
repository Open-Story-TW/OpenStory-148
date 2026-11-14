package domain

import "context"

type MesoDropRequest struct {
	Session     CharacterSession
	OperationID string
	Amount      int64
}

type MesoDropRepository interface {
	DropMesosOnMap(context.Context, MesoDropRequest) (InventoryResult, error)
}
