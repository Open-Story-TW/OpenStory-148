package domain

import "context"

// PhysicalAttackTarget is constructed from the world room's validated spawn;
// Defeated, Experience, position and spawn identity never come from the client.
type PhysicalAttackTarget struct {
	ObjectID         int32
	MobID            int
	SpawnOperationID string
	X, Y, Damage     int
	Defeated         bool
	Experience       int64
}

type PhysicalAttackRequest struct {
	Session     CharacterSession
	OperationID string
	ClientTick  uint32
	SkillID     int
	Targets     []PhysicalAttackTarget
}

type PhysicalAttackRepository interface {
	ApplyPhysicalSkillAttack(context.Context, PhysicalAttackRequest) (SkillAttackResult, error)
}
