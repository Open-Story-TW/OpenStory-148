package domain

import "context"

// MesoConsumeRequest retires the actor's visible ground mesos for a skill
// (Meso Explosion). All drops must be live mesos in the actor's room.
type MesoConsumeRequest struct {
	Session     CharacterSession
	OperationID string
	SkillID     int
	ObjectIDs   []int32
}

// SkillMesoSpawnRequest creates mesos on the ground from a skill (Pickpocket,
// Steal). The actor owns the drop; no balance changes until pickup.
type SkillMesoSpawnRequest struct {
	Session     CharacterSession
	OperationID string
	SkillID     int
	X, Y        int
	Amount      int64
}

// MobStealRequest takes one item from a live monster spawn's drop table
// (Steal). Each spawn can be stolen from once; nil drop means nothing taken.
type MobStealRequest struct {
	Session          CharacterSession
	OperationID      string
	SkillID          int
	MobID            int
	SpawnOperationID string
	X, Y             int
	Boss             bool // From the live spawn's template; bosses are rejected.
}

type SkillMesoRepository interface {
	ConsumeMesoDrops(context.Context, MesoConsumeRequest) ([]ItemDrop, error)
	// PeekMesoDrops reads the named live meso drops without consuming them.
	PeekMesoDrops(context.Context, MesoConsumeRequest) ([]ItemDrop, error)
	SpawnSkillMesos(context.Context, SkillMesoSpawnRequest) (ItemDrop, error)
	StealMobItem(context.Context, MobStealRequest) (*ItemDrop, error)
}
