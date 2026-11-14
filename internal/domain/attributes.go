package domain

import "context"

type AttributeRequest struct {
	Session     CharacterSession
	ClientTick  uint32
	Stat        uint64
	Allocations []AttributeAllocation
}

// AttributeAllocation is one entry in the native automatic AP request.
type AttributeAllocation struct {
	Stat   uint64 `json:"stat"`
	Amount int32  `json:"amount"`
}

type AttributeResult struct {
	Character Character
	Replayed  bool
}

type AttributeRepository interface {
	AllocateAttribute(context.Context, AttributeRequest) (AttributeResult, error)
}
