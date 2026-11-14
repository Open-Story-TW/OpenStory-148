package pirate

import (
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
)

// SummonMovementType values (Java MapleStatEffect.getSummonMovementType).
const (
	stationary       = 0
	follow           = 1
	circleStationary = 5
)

// summon is one pirate summon. Java Legacy knows the octopus as 5211014; v145
// ships it as 5211001, so it follows 5211014's rules. All pirate summons are
// attackers (getSummonType 1); none is a Java puppet.
type summon struct {
	id    int
	stat  buffstat.Stat // Java statup of the summon buff.
	move  uint8
	multi bool // Java MapleSummon.isMultiSummon
	// single removes the summon after its first attack.
	single    bool
	maxDamage func(combat.DamageContext) int
	// mobs is the WZ summon/attack1/info mobCount when the skill level has
	// none (Gaviota's explosion hits up to 3).
	mobs int
}

var summons = []summon{
	{id: 5211001, stat: "PUPPET", move: stationary, multi: true},                              // 章魚砲台 Octopus
	{id: 5211002, stat: "SUMMON", move: circleStationary, multi: true, single: true, mobs: 3}, // 海鷗突擊隊 Gaviota
	{id: 5220002, stat: "PUPPET", move: stationary, multi: true},                              // 砲台章魚王 Wrath of the Octopi
	{id: 5321003, stat: "PUPPET", move: stationary, maxDamage: anchorCeiling},                 // 磁錨 Anchor Aweigh
	{id: 5321004, stat: "SUMMON", move: follow},                                               // 雙胞胎猴子 Monkey Militia
}

func init() {
	for _, s := range summons {
		spec := &combat.AttackSpec{Kind: combat.Summoned, MaxDamage: s.maxDamage, Prepare: summonTargets(s.mobs), Summon: &combat.SummonSpec{MoveAbility: s.move, AssistType: 1, Multi: s.multi, SingleAttack: s.single}}
		skills.Register(skills.Handler{ID: s.id, Kind: skills.KindSummon, Cast: valueOne(s.stat), Attack: spec})
	}
	// 心靈控制 Mind Control: the client sends it as a ranged attack (Java
	// rangedAttack; Legacy then refuses it because it disconnected players with
	// error 38). Java HYPNOTIZE is native monster stat index 28, applied with
	// the WZ prop and time.
	skills.Register(skills.Handler{ID: 5221009, Kind: skills.KindAttack | skills.KindMobStatus, Attack: &combat.AttackSpec{Kind: combat.Shoot, Ammo: combat.AmmoNone, OnHit: []combat.MobStatusApply{{Stat: combat.MobDazzle, Value: 1}}}})
}

// summonTargets raises the target limit of a summon attack to the WZ summon
// mobCount; nil when the skill level already carries it.
func summonTargets(mobs int) func(*combat.AttackContext) error {
	if mobs == 0 {
		return nil
	}
	return func(ctx *combat.AttackContext) error {
		ctx.Effect.MobCount = max(ctx.Effect.MobCount, mobs)
		return nil
	}
}

// valueOne is the summon buff: stat 1 for the WZ time.
func valueOne(stat buffstat.Stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, e := buffTime(c)
		if e != nil {
			return p, e
		}
		p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: stat, Value: 1}}}
		return p, nil
	}
}

// anchorCeiling is Java SummonHandler's bound with selfDestruction added to
// damage: maxBase × 5 × (selfDestruction + damage) / 100.
func anchorCeiling(c combat.DamageContext) int {
	def, err := content.SkillDefinition("", c.Effect.SkillID)
	if err != nil {
		return 0
	}
	v, err := def.Values(c.Effect.Level)
	if err != nil {
		return 0
	}
	c.Effect.Damage += v["selfDestruction"]
	return combat.SummonCeiling(c)
}
