package combat

import (
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// SummonSpec describes a summon a skill creates (Java MapleSummon). A skill
// registers AttackSpec{Kind: Summoned, Summon: &SummonSpec{...}}; casting it
// spawns the summon and its 0x100 attacks validate against the same spec.
type SummonSpec struct {
	MoveAbility uint8 // SummonMovementType: 0 stationary, 1 follow, 2 walk, 4 circle follow, 5 circle stationary.
	AssistType  uint8 // getSummonType: 0 none (puppet), 1 attack, 2 heal/buff, 3 follow-attack, 7 counter.
	Stance      byte  // Spawn stance; zero means 4.
	Duration    time.Duration
	// DurationKey reads the lifetime in seconds from this WZ level key
	// (Reaper 32111006 "x") instead of the level's time; Duration wins if set.
	DurationKey string
	// NoCastSpawn: the cast only applies its buff; the summon is spawned
	// elsewhere (AttackEvent.SpawnSummon, e.g. Reaper on kill).
	NoCastSpawn bool
	// SingleAttack removes the summon after its first attack.
	SingleAttack bool
	// ExpiryAction authorizes only the original terminal summon attack action.
	// Its damage, target count, bounds and animation interval come from summon/die.
	ExpiryAction byte
	// Puppet summons absorb monster attacks (Decoy) and have HP.
	Puppet bool
	HP     int
	// Multi allows several summons of the same skill per owner.
	Multi bool
	// Heal is HP restored to party members in range every HealInterval
	// (Beholder/Dark Spirit style); zero disables.
	Heal         int
	HealInterval time.Duration
	// Variants: the spawned summon shows one of these skill IDs at random
	// (Elemental Knights 23111008/9/10); spec, level and cost stay the cast skill's.
	Variants []int
	// Twins are further summons each cast spawns beside the first, shown
	// with their own skill IDs (Monkey Militia's second monkey 5320011);
	// spec, level, lifetime and cost stay the cast skill's.
	Twins []int
	// Sub handles RecvSubSummon (0x102): the client asks the summon to use a
	// linked skill (Beholder 1320008 heal / 1320009 buff). Nil ignores it.
	Sub func(SubSummonContext) (SubSummonResult, error)
}

type SubSummonContext struct {
	Character              domain.Character
	SummonSkill, SummonLvl int
	SubSkill, SubLevel     int // SubLevel is the learned level (>0).
	SubValues              map[string]int
	LastSub, Now           time.Time
}

// SubSummonResult: HealHP restores the owner (transient vitals like
// regeneration); Action is the summon skill animation broadcast to the room;
// Apply runs after the packet outside locks (S0a buffs such as 1320009).
type SubSummonResult struct {
	HealHP int
	Action byte
	Apply  func()
	// Spawn creates child summons at the parent's position (Robot Factory
	// 35121009 -> three 35121011).
	Spawn []SummonChild
}

type SummonChild struct{ SkillID, Level int }

// SummonExpiryEffect reads the self-destruction attack rather than the cast's
// party-buff rectangle or the ordinary summon/attack1 action data.
func SummonExpiryEffect(skill content.Skill, level int) (Effect, time.Duration, error) {
	effect, err := FromSkill(skill, level)
	if err != nil {
		return Effect{}, 0, err
	}
	values, err := skill.Values(level)
	if err != nil {
		return Effect{}, 0, err
	}
	death, damage := skill.SummonDeath, values["selfDestruction"]
	if death == nil || death.MobCount < 1 || death.MobCount > 15 || death.AttackAfter < 1 || death.AttackAfter > 5000 || damage < 1 || damage > 100000 {
		return Effect{}, 0, fmt.Errorf("skill%d level%d invalid summon death data", skill.ID, level)
	}
	lt, rb := death.Range.LT, death.Range.RB
	if lt[0] >= rb[0] || lt[1] >= rb[1] {
		return Effect{}, 0, fmt.Errorf("skill%d invalid summon death bounds", skill.ID)
	}
	effect.Damage, effect.MobCount, effect.AttackCount = damage, death.MobCount, 1
	effect.Box = Box{LT: Point{lt[0], lt[1]}, RB: Point{rb[0], rb[1]}, Set: true}
	return effect, time.Duration(death.AttackAfter) * time.Millisecond, nil
}

// SummonCeiling is Java SummonHandler's bound: maxBase × 5 × damage% / 100.
func SummonCeiling(c DamageContext) int {
	if c.Mob.Invincible || c.Mob.OnlyNormalAttack {
		return 0
	}
	if c.Mob.HasFixedDamage || c.Mob.FixedDamage > 0 {
		return max(0, c.Mob.FixedDamage)
	}
	v := mulDiv(mulDiv(MaxBaseDamage(c.Stats), 5, 1), int64(max(100, c.Effect.Damage)), 100)
	return int(min(v, int64(MaxLineDamage())))
}
