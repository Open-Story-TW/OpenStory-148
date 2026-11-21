package thief

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"sync"
)

// taunt is Java MonsterStatus SHOWDOWN, MDEF and WDEF x (4121003, 4221003).
var taunt = []combat.MobStatusApply{
	{Stat: combat.MobShowdown, ValueFrom: "x"}, {Stat: combat.MobMDR, ValueFrom: "x"}, {Stat: combat.MobPDR, ValueFrom: "x"},
}

// ambush is Java NINJA_AMBUSH (4121004, 4221004): a tick of damage% of max
// base damage every second for the WZ time.
var ambush = combat.MobStatusApply{Stat: combat.MobAmbush, Poison: true, TickFrom: "damage"}

// Shadow Web binds the target and uses the v148 WZ dot, dotInterval and dotTime.
var shadowWeb = combat.MobStatusApply{Stat: combat.MobWeb, Poison: true}

// mobCast is a Java isMonsterBuff skill: statuses on the monsters the cast
// packet names inside the WZ lt/rb box, no damage.
func mobCast(st ...combat.MobStatusApply) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: noAttackPacket, MobCast: &combat.MobCastSpec{Statuses: st, UseBox: true}}
}

// drain (4101005) heals x percent of the damage dealt, at most half of MaxHP
// (Java MapleCharacter.onAttack).
func drain(ctx *combat.AttackContext, dealt int64) (int, int) {
	return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
}

// shadowMesoRange (4111004) is the percent the client's damage routine applies
// on top of the skill's 100% base (it has no WZ damage): it draws r from
// [0.5, 1.5] x moneyCon and scales by 40% of int(r), and prop% of the time by
// a further (100+x)%. The draws are in the client's roll sequence, so the server
// only knows their range.
func shadowMesoRange(e combat.Effect) (low, high int) {
	base := 100 + e.Damage // A learned passive adds its damR here (damageIncrease).
	least, most := 40*(e.MoneyCon/2)/100, 40*(e.MoneyCon*3/2)/100
	if e.Prop > 0 {
		most = most * (100 + e.X) / 100
	}
	return base * least / 100, (base*most + 99) / 100
}

// summonBuff is Java SUMMON 1 for the WZ time; the core spawns the summon.
func summonBuff(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("SUMMON", 1))
}

// darkFlare (4111007, 4211007) is a stationary summon that reflects x% of
// monster contact damage back at the attacker (capped at half the mob's max HP
// in v148; Java SummonMovementType.STATIONARY, getSummonType 7).
func darkFlare() *combat.AttackSpec {
	return &combat.AttackSpec{
		Kind:      combat.Summoned,
		MaxDamage: darkFlareCeiling,
		Summon:    &combat.SummonSpec{MoveAbility: 0, AssistType: 7},
	}
}

func darkFlareCeiling(c combat.DamageContext) int {
	if c.Mob.Invincible || c.Mob.OnlyNormalAttack {
		return 0
	}
	if c.Mob.HasFixedDamage || c.Mob.FixedDamage > 0 {
		return max(0, c.Mob.FixedDamage)
	}
	halfHP := int(min(c.Mob.HP/2, int64(combat.MaxLineDamage())))
	return min(max(halfHP, combat.SummonCeiling(c)), combat.MaxLineDamage())
}

// mirroredTargetSummon (4341006) is a stationary decoy (Java isPuppet,
// getSummonType 0). Its HP is WZ x per level, which SummonSpec cannot carry
// yet.
var mirroredTargetSummon = &combat.AttackSpec{Kind: noAttackPacket, Summon: &combat.SummonSpec{MoveAbility: 0, AssistType: 0, Puppet: true}}

// mirroredTarget (4341006) is PUPPET 1 and requires an active Shadow
// Partner (Java MapleStatEffect.applyTo).
func mirroredTarget(c *skills.CastContext) (skills.CastPlan, error) {
	for _, id := range []int{4331002, 4111002, 4211008} {
		if buffActive(c.Caster, c.Now, id) {
			return selfBuff(c, stat("PUPPET", 1))
		}
	}
	return skills.StandardCost(c), fmt.Errorf("skill%d: needs Shadow Partner", c.Skill.ID)
}

// smokeScreen (4221006) is an invincible area: monsters cannot hurt the
// owner's party inside it (Java MapleMist isPoisonMist 2). The box is the WZ
// common lt/rb, which combat.FromSkill does not carry yet.
var smokeScreen = &combat.AttackSpec{Kind: noAttackPacket, Field: &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaInvincible, PlayersInvincible: true,
	Box: combat.Box{LT: combat.Point{X: -250, Y: -150}, RB: combat.Point{X: 250, Y: 150}, Set: true}}}

// mesoExplosionCeiling (4211006): Legacy DamageParse skips the damage check
// for Meso Explosion; the consumed ground mesos bound the attack instead.
func mesoExplosionCeiling(c combat.DamageContext) int {
	if c.Mob.Invincible || c.Mob.OnlyNormalAttack {
		return 0
	}
	if c.Mob.HasFixedDamage || c.Mob.FixedDamage > 0 {
		return max(0, c.Mob.FixedDamage)
	}
	return combat.MaxLineDamage()
}

// pickpocketAttacks are the attacks Java DamageParse lets Pickpocket steal
// mesos on (skill 0, the normal attack, belongs to S0a's NormalAttack).
var pickpocketAttacks = map[int]bool{4001334: true, 4201005: true, 4211002: true, 4211004: true, 4221003: true, 4221007: true}

// pickpocketOn drops mesos after the listed attacks while Pickpocket is active.
func pickpocketOn(s *combat.AttackSpec) { s.AfterHit = chainAfter(s.AfterHit, pickpocketDrop) }

// pickpocketDrop is Java handlePickPocket: every damage line rolls pickRate
// (4211003 prop + Greed u) and drops min(max(line/20000 × x, 1), x) mesos
// within 50px of the monster.
func pickpocketDrop(e combat.AttackEvent) {
	ch := e.Session.Character
	if !pickpocketAttacks[e.SkillID] || e.SpawnMesos == nil {
		return
	}
	maxMeso := 0
	for _, b := range ch.SkillBuffs {
		if b.ID == 4211003 && b.Stat == "PICKPOCKET" && b.ExpiresAt.After(e.Now) {
			maxMeso = b.Value
		}
	}
	v := values(ch, e.Now, 4211003)
	if maxMeso < 1 || v == nil {
		return
	}
	rate := v["prop"]
	if g := values(ch, e.Now, 4220009); g != nil {
		rate += g["u"]
	}
	for _, h := range e.Hits {
		for _, line := range h.Lines {
			if rate < 100 && !roll(rate) {
				continue
			}
			amount := min(max(int64(line)*int64(maxMeso)/20000, 1), int64(maxMeso))
			_ = e.SpawnMesos(h.X+rollRange(-50, 49), h.Y, amount)
		}
	}
}

// chainAfter runs prior, then f.
func chainAfter(prior, f func(combat.AttackEvent)) func(combat.AttackEvent) {
	if prior == nil {
		return f
	}
	return func(e combat.AttackEvent) { prior(e); f(e) }
}

// chain runs prior, then f.
func chain(prior, f func(*combat.AttackContext) error) func(*combat.AttackContext) error {
	if prior == nil {
		return f
	}
	return func(ctx *combat.AttackContext) error {
		if err := prior(ctx); err != nil {
			return err
		}
		return f(ctx)
	}
}

// steal (4201004) is Java MapleMonster.handleSteal: each hit non-boss monster
// rolls the level's prop for one item from its drop table; the world pins the
// result so every spawn yields at most once.
func steal(e combat.AttackEvent) {
	if e.SkillID != 4201004 || e.StealItem == nil {
		return
	}
	v := values(e.Session.Character, e.Now, 4201004)
	if v == nil {
		return
	}
	for _, h := range e.Hits {
		if !mobBoss(h.MobID) && roll(v["prop"]) {
			_ = e.StealItem(h)
		}
	}
}

// mobBoss reports a boss template; unknown templates count as bosses so
// steal never guesses.
var mobBoss = func(id int) bool {
	catalogOnce.Do(func() { catalog, _ = content.DefaultCatalog() })
	if catalog == nil {
		return true
	}
	m, err := catalog.Mob(id)
	return err != nil || m.Boss
}

var (
	catalogOnce sync.Once
	catalog     *content.Catalog
)

// thrown marks an attack that explodes where it lands (Flashbang): WZ lt/rb
// are centred on the landing point, which the v145 melee packet does not
// carry, so only the distance check is skipped (Java skips it too); mobCount
// and the damage ceiling still apply.
func thrown(s *combat.AttackSpec) { s.IgnoreRange = true }
