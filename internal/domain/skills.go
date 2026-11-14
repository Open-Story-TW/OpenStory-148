package domain

import (
	"context"
	"time"
)

type SkillState struct {
	ID          int       `json:"id"`
	Level       int       `json:"level"`
	MasterLevel int       `json:"master_level"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}
type SkillCooldown struct {
	ID        int       `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SkillBuff struct {
	ID        int       `json:"id"`
	Level     int       `json:"level"`
	Stat      string    `json:"stat"`
	Value     int       `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
	NextTick  time.Time `json:"next_tick,omitempty"`
	// Counter is server-only state of the effect (remaining hits); it is
	// never encoded into a packet.
	Counter int `json:"counter,omitempty"`
}
type SkillAllocateRequest struct {
	Session     CharacterSession
	SkillID     int
	OperationID string
	// Count is how many points to spend; zero means one. The skill takes as
	// many of them as its maximum level and the character's points allow.
	Count int
}
type SkillCastRequest struct {
	Session              CharacterSession
	SkillID, ClientLevel int
	OperationID          string
}
type SkillResult struct {
	Character Character
	Replayed  bool
	Changed   bool
	SkillID   int
	Expired   []SkillBuff
	// Effect is set when a cast took effect and its FX should be shown
	// (a toggle cancellation does not).
	Effect         bool
	Inventory      []InventoryChange // Items consumed by the cast.
	ResetCooldowns []SkillCooldown   // Cooldowns cleared by the cast.
	Updated        []SkillBuff       // Rows whose value or expiry changed in place (Consume, Retime).
	MoveTo         int               // Map the caster is moved to after the cast.
	Cooldowns      []SkillCooldown   // Cooldowns started outside a cast (triggers).
	Applied        int               // Skill whose rows the cast stored (CastPlan.ApplyAs), else 0.
}

// SkillBuffChange replaces (or cancels) one learned skill's buff rows outside
// a cast: combo orbs, energy charge, hit-count buffs, buffs on being hit.
type SkillBuffChange struct {
	SkillID, Level int
	Stats          []SkillBuffStat
	Duration, Tick time.Duration
	Cancel         bool
	Cooldown       time.Duration // Started with the change; 0 keeps the current one.
	Counter        int           // Server-only counter stored on the rows.
	HealHP, HealMP int           // Flat recovery committed with the change.
	CostMP         int           // MP drained with the change (upkeep).
}
type SkillBuffStat struct {
	Stat  string
	Value int
}

// SkillTriggerRepository commits trigger-driven buff changes.
type SkillTriggerRepository interface {
	ChangeSkillBuffs(context.Context, CharacterSession, []SkillBuffChange) (SkillResult, error)
}

type SkillRepository interface {
	AllocateSkill(context.Context, SkillAllocateRequest) (SkillResult, error)
	CastSkill(context.Context, SkillCastRequest) (SkillResult, error)
	CancelSkill(context.Context, CharacterSession, int) (SkillResult, error)
	TickSkillBuffs(context.Context, CharacterSession) (SkillResult, error)
}

// SkillAttackRequest is assembled only after the authoritative world validates
// every target in the native attack. Experience/Defeated never come from wire.
type SkillAttackRequest struct {
	Session          CharacterSession
	OperationID      string
	SpawnOperationID string
	ClientTick       uint32
	SkillID          int
	TargetObjectID   int32
	TargetMobID      int
	TargetX, TargetY int // authoritative mob location used by the pinned loot job.
	Damage           int
	Defeated         bool
	Experience       int64
}
type SkillAttackResult struct {
	Character Character
	Replayed  bool
}
type SkillAttackRepository interface {
	ApplySkillAttack(context.Context, SkillAttackRequest) (SkillAttackResult, error)
}
