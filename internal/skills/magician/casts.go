package magician

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

func init() {
	// Java isMonsterBuff: cast on up to mobCount monsters inside lt/rb, no damage.
	slow := mobCast(combat.MobStatusApply{Stat: combat.MobSpeed, ValueFrom: "x"})
	seal := mobCast(combat.MobStatusApply{Stat: combat.MobSeal, Value: 1})
	skills.Register(skills.Handler{ID: 2101003, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: slow})                                                // Slow
	skills.Register(skills.Handler{ID: 2201003, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: slow})                                                // Slow
	skills.Register(skills.Handler{ID: 2111004, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: seal})                                                // Seal
	skills.Register(skills.Handler{ID: 2211004, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: seal})                                                // Seal
	skills.Register(skills.Handler{ID: 2311005, Kind: skills.KindMobStatus, Cast: doom, Attack: mobCast(combat.MobStatusApply{Stat: combat.MobDoom, Value: 1})}) // Doom

	// Poison Mist: the v145 client sends it as a magic attack (0x4D, as Java
	// MagicDamage then effect.applyTo(pos)); the area is a poison mist (Java
	// MapleMist type 1) at the caster for time, poisoning monsters inside
	// every second.
	mist := magicSpec(2111003)
	mist.Field = &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaPoison, Box: box(2111003), MobStatus: []combat.MobStatusApply{poison}}
	skills.Register(skills.Handler{ID: 2111003, Kind: skills.KindAttack | skills.KindFieldObject | skills.KindMobStatus, Attack: mist})
	// Mist Eruption also detonates the caster's Poison Mists (Java isMistEruption).
	eruption := magicSpec(2121003)
	eruption.EruptAreas = []int{2111003}
	skills.Register(skills.Handler{ID: 2121003, Kind: skills.KindAttack, Attack: eruption})

	// Summons follow their owner and attack (Java FOLLOW, getSummonType 1).
	summon(2121005, stun)   // Elquines
	summon(2221005, freeze) // Ifrit
	summon(2321003)         // Bahamut

	// Heal: the cast heals the party, the attack packet only damages undead
	// monsters (Java DamageParse).
	heal := magicSpec(2301002)
	heal.MaxDamage = undeadOnly
	// The cast already paid mpCon; Java's MagicDamage skips applyTo (the MP
	// cost) for Heal's attack packet, which the client sends after every cast.
	element := heal.Prepare
	heal.Prepare = func(ctx *combat.AttackContext) error {
		ctx.Effect.MPCon = 0
		return element(ctx)
	}
	skills.Register(skills.Handler{ID: 2301002, Kind: skills.KindHeal | skills.KindPartyBuff | skills.KindAttack, Cast: healCast, Attack: heal})
	skills.Register(skills.Handler{ID: 2311001, Kind: skills.KindHeal | skills.KindPartyBuff, Cast: dispel}) // Dispel

	// Mystic Door: the town portal pair, one 4006000, and Java's hacked
	// SOULARROW icon for time.
	skills.Register(skills.Handler{ID: 2311002, Kind: skills.KindFieldObject, Cast: mysticDoor, Attack: &combat.AttackSpec{Field: &combat.FieldSpec{Kind: combat.FieldTownPortal}}})
}

// mobCast is a no-damage monster cast inside the WZ lt/rb.
func mobCast(status combat.MobStatusApply) *combat.AttackSpec {
	return &combat.AttackSpec{MobCast: &combat.MobCastSpec{UseBox: true, Statuses: []combat.MobStatusApply{status}}}
}

// box is the level-independent WZ lt/rb of a skill.
func box(id int) combat.Box {
	def, err := content.SkillDefinition("", id)
	if err != nil {
		panic(err)
	}
	r, ok := def.RangeAt(1)
	if !ok {
		panic("magician: no lt/rb")
	}
	return combat.Box{LT: combat.Point{X: r.LT[0], Y: r.LT[1]}, RB: combat.Point{X: r.RB[0], Y: r.RB[1]}, Set: true}
}

// summon registers an attacking summon and its SUMMON buff.
func summon(id int, hits ...hit) {
	spec := attackSpec(id, combat.Summoned, hits...)
	spec.Summon = &combat.SummonSpec{MoveAbility: 1, AssistType: 1}
	h := skills.Handler{ID: id, Kind: skills.KindSummon, Cast: buff("SUMMON", "one"), Attack: spec}
	if id == 2321003 {
		h.Passive = summonASR
	} else {
		h.Passive = summonTER
	}
	skills.Register(h)
}

// undeadOnly is Heal's ceiling: Java returns before damaging a living monster.
func undeadOnly(c combat.DamageContext) int {
	if !c.Mob.Undead {
		return 0
	}
	return combat.LineCeiling(c)
}

// doom (2311005) pays mpCon and cooltime; WZ itemConNo 0 takes no 4006000
// (Java haveItem/removeById with 0).
func doom(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("itemConNo") < 1 {
		p.Items = nil
	}
	return p, nil
}

// healCast (2301002) heals every party member in range by a roll between 3
// and 5 times hp% of the caster's magic attack (Java makeHealHP).
func healCast(c *skills.CastContext) (skills.CastPlan, error) {
	stats, err := combat.AttackStats(c.Caster, c.Now)
	if err != nil {
		return skills.CastPlan{}, err
	}
	low, high := stats.MATK*3*c.Value("hp")/100, stats.MATK*5*c.Value("hp")/100
	p := skills.StandardCost(c)
	p.Heal = &skills.Heal{HP: low + rand.IntN(high-low+1)}
	return skills.PartyPlan(c, &skills.Buff{}, p)
}

// dispel (2311001) removes the abnormal statuses of the party in range with
// prop% (Java isDispel and makeChanceResult, rolled once per cast).
func dispel(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Dispel = roll(c.Value("prop"))
	return skills.PartyPlan(c, &skills.Buff{}, p)
}

// mysticDoor (2311002) pays 4006000 and shows SOULARROW 0 for time while the
// core opens the town portal.
func mysticDoor(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("time") < 1 || len(p.Items) != 1 {
		return p, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{{Stat: "SOULARROW", Value: 0}}}
	return p, nil
}
