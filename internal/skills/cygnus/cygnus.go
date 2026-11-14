package cygnus

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"os"
	"time"
)

// Java Legacy is the reference: MapleStatEffect (statups, monsterStatus,
// summon movement), PlayerStats (passives), PlayerHandler/DamageParse
// (attacks) and MapleCharacter (combo, energy charge, drains).

func stat(name buffstat.Stat, v int) skills.StatValue { return skills.StatValue{Stat: name, Value: v} }

// genericStats is Java MapleStatEffect's WZ-key statups of every overTime
// effect that is neither a summon nor Energy Charge (pad, pdd, ..., e*,
// indie*); skills.StandardStats covers the first ten.
var genericStats = []struct {
	key  string
	stat buffstat.Stat
}{
	{"emhp", "ENHANCED_MAXHP"}, {"emmp", "ENHANCED_MAXMP"}, {"epad", "ENHANCED_WATK"}, {"epdd", "ENHANCED_WDEF"}, {"emdd", "ENHANCED_MDEF"},
	{"str", "STR"}, {"dex", "DEX"}, {"int", "INT"}, {"luk", "LUK"},
	{"indiePad", "ANGEL_ATK"}, {"indieMad", "ANGEL_MATK"}, {"indieMhp", "HP_BOOST"}, {"indieMmp", "MP_BOOST"},
	{"indieJump", "ANGEL_JUMP"}, {"indieSpeed", "ANGEL_SPEED"}, {"indieAcc", "ANGEL_ACC"}, {"indieEva", "ANGEL_AVOID"}, {"indieAllStat", "ANGEL_STAT"},
}

// withGeneric appends the generic statups the special stats do not already
// carry.
func withGeneric(values map[string]int, special []skills.StatValue) []skills.StatValue {
	out := append([]skills.StatValue(nil), special...)
	has := func(s buffstat.Stat) bool {
		for _, v := range out {
			if v.Stat == s {
				return true
			}
		}
		return false
	}
	generic := skills.StandardStats(values)
	for _, g := range genericStats {
		if v := values[g.key]; v != 0 {
			generic = append(generic, stat(g.stat, v))
		}
	}
	for _, g := range generic {
		if !has(g.Stat) {
			out = append(out, g)
		}
	}
	return out
}

// buff is the standard cost plus the special stats and the generic WZ
// statups on the caster for the WZ time.
func buff(c *skills.CastContext, special ...skills.StatValue) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	stats := withGeneric(c.Values, special)
	if c.Value("time") < 1 || len(stats) == 0 {
		return p, fmt.Errorf("skill%d: no buff", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: stats}
	return p, nil
}

// value is a buff whose Java statup takes one WZ key (BOOSTER x).
func value(name buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if c.Value(key) == 0 && name != "BOOSTER" {
			return skills.StandardCost(c), fmt.Errorf("skill%d: no %s", c.Skill.ID, key)
		}
		return buff(c, stat(name, c.Value(key)))
	}
}

// party moves the buff of cast to the party members inside the WZ lt/rb
// (Java isPartyBuff).
func party(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p, e := cast(c)
		if e != nil {
			return p, e
		}
		return skills.PartyPlan(c, p.Self, p)
	}
}

// generic is the Cast of a buff made of the generic statups only.
func generic(c *skills.CastContext) (skills.CastPlan, error) { return buff(c) }

// summonBuff is the summon's Java statup (SUMMON or PUPPET 1) for the WZ
// time; the core spawns the summon from the registered SummonSpec.
func summonBuff(name buffstat.Stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		if c.Value("time") < 1 {
			return p, fmt.Errorf("skill%d: no duration", c.Skill.ID)
		}
		p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{stat(name, 1)}}
		return p, nil
	}
}

// learned is the live level of a learned skill, 0 when not learned.
func learned(ch domain.Character, now time.Time, id int) int {
	for _, s := range ch.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// values are the WZ values of a learned skill, nil when not learned.
func values(ch domain.Character, now time.Time, id int) map[string]int {
	level := learned(ch, now, id)
	if level < 1 {
		return nil
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil {
		return nil
	}
	v, err := def.Values(min(level, def.MaxLevel))
	if err != nil {
		return nil
	}
	return v
}

// activeBuff returns the first of ids with a live buff, 0 when none.
func activeBuff(ch domain.Character, now time.Time, ids ...int) int {
	for _, b := range ch.SkillBuffs {
		if b.ExpiresAt.After(now) {
			for _, id := range ids {
				if b.ID == id {
					return id
				}
			}
		}
	}
	return 0
}

// weaponType is the equipped weapon's item category (itemID / 10000), 0
// when bare-handed.
func weaponType(ch domain.Character) int {
	for _, e := range ch.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

// Weapon categories (Java GameConstants.getWeaponType).
const (
	oneHandedSword = 130
	oneHandedAxe   = 131
	oneHandedBlunt = 132
	wand           = 137
	staff          = 138
	twoHandedSword = 140
	twoHandedAxe   = 141
	twoHandedBlunt = 142
	bow            = 145
	claw           = 147
	knuckle        = 148
)

// mastery is Java CalcPassive_Mastery: the skill's mastery plus x accuracy
// ("acc") or x magic attack ("matk") while one of the weapons is equipped.
func mastery(xAs string, weapons ...int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		w := weaponType(c.Character)
		for _, ok := range weapons {
			if w == ok {
				b := skills.Bonus{Mastery: c.Values["mastery"]}
				if xAs == "matk" {
					b.MATK = c.Values["x"]
				} else {
					b.ACC = c.Values["x"]
				}
				return b
			}
		}
		return skills.Bonus{}
	}
}

// critical is a Java critSkill passive: rate key and criticaldamageMin.
func critical(rateKey string) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{Critical: c.Values[rateKey], CriticalDamageMin: c.Values["criticaldamageMin"]}
	}
}

// physicalTraining is Java localstr/localdex += strX/dexX (localluk lukX).
func physicalTraining(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"], LUK: c.Values["lukX"]}
}

func maxHP(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MaxHPPercent: c.Values["mhpR"]}
}

// accAvoid is +x accuracy and +y avoidability.
func accAvoid(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{ACC: c.Values["x"], AVOID: c.Values["y"]}
}

// pvpOnly registers a passive Legacy only uses in PvP (PlayerStats.pvpSkills)
// or never reads; it has no PvE effect.
func pvpOnly(id int) { skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive}) }

func passive(id int, f func(*skills.PassiveContext) skills.Bonus) {
	skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
}

// roll succeeds with prop percent; rollDie is 1..faces. Tests replace them.
var (
	roll    = func(prop int) bool { return rand.IntN(100) < prop }
	rollDie = func(faces int) int { return rand.IntN(faces) + 1 }
)

// Attack builders.

type option func(*combat.AttackSpec)

func attack(kind combat.AttackKind, opts ...option) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: kind}
	for _, o := range opts {
		o(s)
	}
	return s
}

func melee(opts ...option) *combat.AttackSpec { return attack(combat.Melee, opts...) }
func shoot(opts ...option) *combat.AttackSpec { return attack(combat.Shoot, opts...) }
func magic(opts ...option) *combat.AttackSpec { return attack(combat.Magic, opts...) }

// hit adds Java monsterStatus entries rolled with the level's prop.
func hit(st ...combat.MobStatusApply) option {
	return func(s *combat.AttackSpec) { s.OnHit = append(s.OnHit, st...) }
}

// prepare chains a caster-dependent step before planning.
func prepare(f func(*combat.AttackContext) error) option {
	return func(s *combat.AttackSpec) {
		prior := s.Prepare
		if prior == nil {
			s.Prepare = f
			return
		}
		s.Prepare = func(ctx *combat.AttackContext) error {
			if err := prior(ctx); err != nil {
				return err
			}
			return f(ctx)
		}
	}
}

// noAmmo marks skills that consume no projectile (Java rangedAttack AOE).
func noAmmo(s *combat.AttackSpec) { s.Ammo = combat.AmmoNone }

// drainX heals x percent of the damage dealt, at most half of MaxHP (Java
// MapleCharacter.onAttack).
func drainX(s *combat.AttackSpec) {
	s.Drain = func(ctx *combat.AttackContext, dealt int64) (int, int) {
		return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
	}
}

// element fixes the attack element (WZ elemAttr; the seed lacks it).
func element(e combat.Element) option {
	return prepare(func(ctx *combat.AttackContext) error {
		ctx.Effect.Element = e
		return nil
	})
}

var (
	stun   = combat.MobStatusApply{Stat: combat.MobStun, Value: 1}
	poison = combat.MobStatusApply{Stat: combat.MobPoison, Value: 1, Poison: true}
)

// noAttackPacket is the Kind of specs that only carry a summon, a field
// object or a monster cast.
const noAttackPacket combat.AttackKind = 0

// mobCast is a Java isMonsterBuff skill: statuses on the monsters the cast
// packet names inside the WZ lt/rb, no damage.
func mobCast(st ...combat.MobStatusApply) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: noAttackPacket, MobCast: &combat.MobCastSpec{Statuses: st, UseBox: true}}
}

// box is the level-independent WZ lt/rb of a skill.
func box(id int) combat.Box {
	def, err := content.SkillDefinition("", id)
	if err != nil {
		panic(err)
	}
	r, ok := def.RangeAt(1)
	if !ok {
		panic(fmt.Sprintf("cygnus: skill%d has no lt/rb", id))
	}
	return combat.Box{LT: combat.Point{X: r.LT[0], Y: r.LT[1]}, RB: combat.Point{X: r.RB[0], Y: r.RB[1]}, Set: true}
}

// poisonMist is Java MapleMist isPoisonMist 1 at the caster for the WZ time
// (Flame Gear 12111005, Poison Bomb 14111006): monsters inside are poisoned.
func poisonMist(id int) option {
	return func(s *combat.AttackSpec) {
		s.Field = &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaPoison, Box: box(id), MobStatus: []combat.MobStatusApply{poison}}
	}
}

// SummonMovementType values (Java MapleStatEffect.getSummonMovementType).
const (
	stationary = 0
	follow     = 1
)

// spirit is a Cygnus first-job spirit (and Ifrit): follows its owner and
// attacks (Java FOLLOW, getSummonType 1, statup SUMMON 1).
func spirit(id int, e combat.Element) skills.Handler {
	spec := &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: follow, AssistType: 1}}
	element(e)(spec)
	return skills.Handler{ID: id, Kind: skills.KindSummon, Cast: summonBuff("SUMMON"), Attack: spec}
}

// morph is a Cygnus transformation (Java isPirateMorph): STANCE 100, the
// morph id (getMorph(chr): +100 for female characters) and the generic
// statups.
func morph(id int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		m := id
		if c.Caster.Gender == 1 {
			m += 100
		}
		return buff(c, stat("STANCE", 100), stat("MORPH", m))
	}
}
