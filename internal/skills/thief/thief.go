package thief

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"os"
	"time"
)

// sec is a WZ value in seconds.
func sec(c *skills.CastContext, key string) time.Duration {
	return time.Duration(c.Value(key)) * time.Second
}

func stat(name buffstat.Stat, value int) skills.StatValue {
	return skills.StatValue{Stat: name, Value: value}
}

// selfBuff is the standard cost and the given stats on the caster for the WZ
// time.
func selfBuff(c *skills.CastContext, stats ...skills.StatValue) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("time") < 1 {
		return p, fmt.Errorf("skill%d: no time", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: sec(c, "time"), Stats: stats}
	return p, nil
}

// partyBuff is selfBuff for the party members inside the WZ lt/rb range
// (Java isPartyBuff).
func partyBuff(c *skills.CastContext, stats ...skills.StatValue) (skills.CastPlan, error) {
	p, e := selfBuff(c, stats...)
	if e != nil {
		return p, e
	}
	return skills.PartyPlan(c, p.Self, p)
}

// costOnly is a cast whose effect the core applies from the attack spec
// (monster cast, mist).
var costOnly = skills.CostOnly

// noAttackPacket is the Kind of specs that only carry a summon, a field
// object or a monster cast: they never accept an attack packet.
const noAttackPacket combat.AttackKind = 0

// attack builds a spec for the native family kind (Java closeRangeAttack or
// rangedAttack); opts add statuses and caster-dependent preparation.
func attack(kind combat.AttackKind, opts ...func(*combat.AttackSpec)) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: kind}
	for _, o := range opts {
		o(s)
	}
	return s
}

func melee(opts ...func(*combat.AttackSpec)) *combat.AttackSpec {
	return attack(combat.Melee, append(opts, thiefAttack)...)
}
func shoot(opts ...func(*combat.AttackSpec)) *combat.AttackSpec {
	return attack(combat.Shoot, append(opts, thiefAttack)...)
}

// hit adds Java MapleStatEffect monsterStatus entries, rolled with the
// level's prop for its time.
func hit(st ...combat.MobStatusApply) func(*combat.AttackSpec) {
	return func(s *combat.AttackSpec) { s.OnHit = append(s.OnHit, st...) }
}

// prepare chains caster-dependent steps before planning.
func prepare(f func(*combat.AttackContext) error) func(*combat.AttackSpec) {
	return func(s *combat.AttackSpec) {
		prior := s.Prepare
		s.Prepare = func(ctx *combat.AttackContext) error {
			if prior != nil {
				if err := prior(ctx); err != nil {
					return err
				}
			}
			return f(ctx)
		}
	}
}

// noAmmo marks attacks that never use stars (Taunt, Shadow Meso).
func noAmmo(s *combat.AttackSpec) { s.Ammo = combat.AmmoNone }

// stun is Java MonsterStatus.STUN = 1.
var stun = combat.MobStatusApply{Stat: combat.MobStun, Value: 1}

// learned returns the caster's level of a skill, 0 when not learned.
func learned(ch domain.Character, now time.Time, id int) int {
	for _, s := range ch.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// buffActive reports a live buff from skill id.
func buffActive(ch domain.Character, now time.Time, id int) bool {
	for _, b := range ch.SkillBuffs {
		if b.ID == id && b.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// values returns the WZ values of a learned passive, nil when not learned.
func values(ch domain.Character, now time.Time, id int) map[string]int {
	level := learned(ch, now, id)
	if level < 1 {
		return nil
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil {
		return nil
	}
	v, err := def.Values(level)
	if err != nil {
		return nil
	}
	return v
}

// roll is a server-side chance in percent.
var roll = func(prop int) bool { return rand.IntN(100) < prop }

// rollRange is a uniform integer in [lo, hi]; tests replace it.
var rollRange = func(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + rand.IntN(hi-lo+1)
}

// venomAttacks are the attacks Java DamageParse lets Venom poison on hit.
var venomAttacks = map[int]bool{
	4301001: true, 4311002: true, 4311003: true, 4331000: true, 4331005: true, 4341002: true, 4341004: true,
	4331006: true, 4341009: true, 4221007: true, 4221001: true, 4211002: true, 4201005: true, 4001002: true,
	4001334: true, 4121007: true, 4111005: true, 4001344: true,
}

// venom adds the first learned Venom (4120005, 4220005, 4340001) as a poison
// rolled with its prop: tick dot% of max base damage for dotTime (Java
// MapleMonster.applyStatus). The v148 books split it into a third job skill
// and a fourth job one that replaces it; the strongest learned one counts.
func venom(ctx *combat.AttackContext) error {
	for _, id := range []int{4120011, 4220011, 4110011, 4210010, 4120005, 4220005, 4340001} {
		v := values(ctx.Character, ctx.Now, id)
		if v == nil {
			continue
		}
		tick := max(1, int64(v["dot"])*combat.MaxBaseDamage(ctx.Stats)/100)
		ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{
			Stat: combat.MobPoison, Poison: true, Value: int(min(tick, 0x7fffffff)),
			Prop: max(1, v["prop"]), Duration: time.Duration(v["dotTime"]) * time.Second,
		})
		return nil
	}
	return nil
}

// damageIncrease is Java PlayerStats.damageIncrease: a learned passive adds
// its WZ value to the damage percent of these attacks.
var damageIncrease = map[int]struct {
	source int
	key    string
}{
	4001344: {4120010, "damR"}, 4101005: {4120010, "damR"}, 4111004: {4120010, "damR"}, 4111005: {4120010, "damR"},
	4201005: {4221007, "damR"}, 4201004: {4221007, "damR"}, 4211002: {4221007, "damR"},
	4211006: {4220009, "x"},
}

func boosted(ctx *combat.AttackContext) error {
	if d, ok := damageIncrease[ctx.Effect.SkillID]; ok {
		if v := values(ctx.Character, ctx.Now, d.source); v != nil {
			ctx.Effect.Damage += v[d.key]
		}
	}
	return nil
}

// stars is the star rule of claw attacks: Shadow Stars (4121006) consumes
// none while active; a Night Lord's Expert Throwing Star Handling (4120010)
// keeps the stars with its prop (Java PlayerHandler.rangedAttack).
func stars(ctx *combat.AttackContext) error {
	if buffActive(ctx.Character, ctx.Now, 4121006) || buffActive(ctx.Character, ctx.Now, 4111009) {
		ctx.NoAmmo = true
	} else if v := values(ctx.Character, ctx.Now, 4120010); ctx.Character.Job == 412 && v != nil && roll(v["prop"]) {
		ctx.NoAmmo = true
	} else if v := values(ctx.Character, ctx.Now, 4110012); ctx.Character.Job/10 == 41 && v != nil && roll(v["prop"]) {
		ctx.NoAmmo = true // v148 鏢術精通
	}
	return nil
}

// thiefAttack is the per-skill preparation every registered thief attack
// runs: Venom, passive damage increases and star rules.
func thiefAttack(s *combat.AttackSpec) {
	prepare(func(ctx *combat.AttackContext) error {
		if venomAttacks[ctx.Effect.SkillID] {
			if err := venom(ctx); err != nil {
				return err
			}
		}
		if err := boosted(ctx); err != nil {
			return err
		}
		if ctx.Kind == combat.Shoot && s.Ammo != combat.AmmoNone {
			return stars(ctx)
		}
		return nil
	})(s)
}

// weaponType is the equipped weapon's item category (itemID/10000, Java
// GameConstants.getWeaponType): 133 dagger, 134 katara, 147 claw.
func weaponType(c domain.Character) int {
	for _, e := range c.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

const (
	weaponDagger = 133
	weaponKatara = 134
	weaponClaw   = 147
)

// dualBlade reports a Dual Blade job (430–434).
func dualBlade(job int) bool { return job >= 430 && job <= 434 }
