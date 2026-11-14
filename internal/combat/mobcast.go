package combat

import (
	"sync"
	"time"

	"maple-go-server/internal/domain"
)

// MobCastSpec is a skill cast on monsters (RecvSkillCast carrying monster
// object IDs) that deals no damage: Threaten/Armor Crash style debuffs,
// Hypnotize, and Dispel (Java MapleStatEffect.applyMonsterBuff / dispel).
type MobCastSpec struct {
	Statuses []MobStatusApply
	// Dispel removes monster buffs (power/guard up, immunities, counters).
	Dispel bool
	// UseBox limits targets to the effect lt/rb box at the caster; otherwise
	// targets must be within Effect.Range (or 400px).
	UseBox bool
	// WholeField targets every live monster in the field (Accelerator
	// 35111005); SkipBoss leaves bosses out.
	WholeField, SkipBoss bool
}

// DispelStats are the monster buffs Dispel/Threaten-family skills remove.
var DispelStats = []MobStat{MobPowerUp, MobMagicUp, MobPGuardUp, MobMGuardUp, MobHardSkin, MobPImmune, MobMImmune, MobPCounter, MobMCounter}

// AttackEvent is published after an attack is applied (committed or, for
// nonlethal no-cost attacks, applied in memory). S0a registers observers for
// counters that grow on hit: combo, energy charge, homing beacon marks.
type AttackEvent struct {
	Session domain.CharacterSession
	Channel int
	SkillID int
	Kind    AttackKind
	Summon  bool
	// SummonSkillID is the attacking summon's shown (variant) skill.
	SummonSkillID int
	Targets       []int32 // Object IDs that took damage.
	Lines         int     // Damage lines per target (energy charge counts targets × lines).
	Damage        int64
	Defeated      []int32
	Now           time.Time
	// Hits carries per-target applied lines and positions (Pickpocket).
	Hits []HitInfo
	// SpawnMesos drops skill mesos owned by the attacker at (x, y); nil when
	// the world has no drop repository. Safe to call from AfterHit.
	SpawnMesos func(x, y int, amount int64) error
	// StealItem takes one ordinary item from the hit monster's drop table
	// (Steal); each spawn yields at most once. The caller rolls prop.
	StealItem func(h HitInfo) error
	// SpawnSummon creates a registered summon for the attacker (Revive
	// 32111006 reapers); SummonCount reports the attacker's live summons of
	// a skill (0 = all) in this field.
	SpawnSummon func(skillID, level int) error
	SummonCount func(skillID int) int
	// GainForce shows Demon Force absorption orbs from a monster to the
	// attacker (0xE2); the DF itself is S0a's resource.
	GainForce func(objectID int32, count, color int) error
}

type HitInfo struct {
	ObjectID         int32
	MobID            int
	SpawnOperationID string
	Boss             bool
	X, Y             int
	Lines            []int
}

var (
	observerMu sync.RWMutex
	observers  []func(AttackEvent)
)

// ObserveAttacks registers a post-attack callback. Callbacks must not block
// and must not call back into the world synchronously while holding locks.
func ObserveAttacks(f func(AttackEvent)) {
	observerMu.Lock()
	observers = append(observers, f)
	observerMu.Unlock()
}

// PublishAttack runs every observer; the world calls it outside its locks.
func PublishAttack(e AttackEvent) {
	observerMu.RLock()
	fs := append([]func(AttackEvent){}, observers...)
	observerMu.RUnlock()
	for _, f := range fs {
		f(e)
	}
}

// ServerHit is server-computed damage on one monster (counter-attacks such
// as 3120010 / 1320011), applied through the world's DamageMobs.
type ServerHit struct {
	ObjectID int32
	Damage   int
}
