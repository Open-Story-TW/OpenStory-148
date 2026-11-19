package magician

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"slices"
	"time"
)

// hit is a Java MapleStatEffect monster status a magic attack inflicts on hit,
// rolled with the level's prop.
type hit uint8

const (
	freeze hit = iota + 1 // FREEZE for twice the WZ time (Java doubles every freeze).
	stun                  // STUN for the WZ time.
)

// elements is the WZ elemAttr of the magician attacks (Skill.wz 2xx.img; the
// skill seed does not carry it).
var elements = map[int]combat.Element{
	2101004: combat.Fire, 2101005: combat.Poison, 2111002: combat.Fire, 2111003: combat.Poison, 2111006: combat.Fire,
	2121001: combat.Fire, 2121003: combat.Poison, 2121005: combat.Fire, 2121006: combat.Poison, 2121007: combat.Fire,
	2201004: combat.Ice, 2201005: combat.Lightning, 2211002: combat.Ice, 2211003: combat.Lightning, 2211006: combat.Ice,
	2221001: combat.Ice, 2221003: combat.Ice, 2221005: combat.Ice, 2221006: combat.Lightning, 2221007: combat.Ice,
	2301002: combat.Holy, 2301005: combat.Holy, 2311004: combat.Holy, 2321001: combat.Holy, 2321003: combat.Holy,
	2321007: combat.Holy, 2321008: combat.Holy,
}

// summonStatusTime is the duration Java SummonHandler gives summon statuses.
const summonStatusTime = 4 * time.Second

// poison is Java's POISON for every skill with a WZ dot: dot% of the max base
// damage every dotInterval for dotTime.
var poison = combat.MobStatusApply{Stat: combat.MobPoison, Value: 1, Poison: true}

// hasDot reports a WZ dot on the skill.
func hasDot(id int) bool {
	def, err := content.SkillDefinition("", id)
	if err != nil {
		panic(err)
	}
	if def.Common != nil {
		return def.Common["dot"] != ""
	}
	for _, v := range def.Levels {
		if v["dot"] != "" {
			return true
		}
	}
	return false
}

// attackSpec is the spec of a magician attack of the given input kind: the WZ
// element (neutral while Elemental Reset is on, Java DamageParse), the hit
// statuses and Java's DoT.
func attackSpec(id int, kind combat.AttackKind, hits ...hit) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: kind}
	if hasDot(id) {
		s.OnHit = append(s.OnHit, poison)
	}
	freezes := false
	for _, h := range hits {
		switch {
		case kind == combat.Summoned:
			// Java SummonHandler applies a summon's statuses for a fixed 4 s.
			stat := combat.MobStun
			if h == freeze {
				stat = combat.MobFreeze
			}
			s.OnHit = append(s.OnHit, combat.MobStatusApply{Stat: stat, Value: 1, Duration: summonStatusTime})
		case h == stun:
			s.OnHit = append(s.OnHit, combat.MobStatusApply{Stat: combat.MobStun, Value: 1})
		case h == freeze:
			freezes = true
		}
	}
	element := elements[id]
	s.Prepare = func(ctx *combat.AttackContext) error {
		ctx.Effect.Element = element
		if activeBuff(ctx.Character, ctx.Now, 2111008, 2211008) {
			ctx.Effect.Element = combat.Physical
		}
		if freezes {
			ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Value: 1, Duration: 2 * ctx.Effect.Time})
		}
		return nil
	}
	return s
}

// magicSpec is a magic attack that also feeds MP Eater.
func magicSpec(id int, hits ...hit) *combat.AttackSpec {
	s := attackSpec(id, combat.Magic, hits...)
	s.Drain = mpEater
	return s
}

// magic registers a magic attack.
func magic(id int, hits ...hit) {
	kind := skills.KindAttack
	if len(hits) > 0 {
		kind |= skills.KindMobStatus
	}
	skills.Register(skills.Handler{ID: id, Kind: kind, Attack: magicSpec(id, hits...)})
}

// mpEaters is GameConstants.getMPEaterForJob.
var mpEaters = map[int]int{210: 2100000, 211: 2100000, 212: 2100000, 220: 2200000, 221: 2200000, 222: 2200000, 230: 2300000, 231: 2300000, 232: 2300000}

// roll succeeds with prop percent.
var roll = func(prop int) bool { return rand.IntN(100) < prop }

// mpEater is MP Eater (Java MapleStatEffect.applyPassive): for each damaged
// non-boss monster, with x% chance, the caster absorbs x% of its max MP. The
// world does not track monster MP, so a monster is taken as full.
func mpEater(ctx *combat.AttackContext, dealt int64) (hp, mp int) {
	id := mpEaters[ctx.Character.Job]
	lv := learned(ctx.Character, id, ctx.Now)
	if lv < 1 || dealt < 1 {
		return 0, 0
	}
	_, v, err := values(id, lv)
	if err != nil {
		return 0, 0
	}
	for _, t := range ctx.Targets {
		if !t.Mob.Boss && roll(v["x"]) {
			mp += t.Mob.MP * v["x"] / 100
		}
	}
	return 0, mp
}

// learned is the live level of a learned skill, 0 when not learned.
func learned(ch domain.Character, id int, now time.Time) int {
	for _, s := range ch.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// values are the WZ values of a skill level.
func values(id, level int) (content.Skill, map[string]int, error) {
	def, err := content.ConfiguredSkillDefinition(id)
	if err != nil {
		return def, nil, err
	}
	v, err := def.Values(level)
	return def, v, err
}

// activeBuff reports a live buff from any of ids.
func activeBuff(ch domain.Character, now time.Time, ids ...int) bool {
	for _, b := range ch.SkillBuffs {
		if b.ExpiresAt.After(now) && slices.Contains(ids, b.ID) {
			return true
		}
	}
	return false
}

// buff is a Cast that gives stat the WZ value key (the skill level for
// "level", 1 for "one") for time seconds, Java MapleStatEffect's per-skill statups.
// The generic WZ stats (pad, pdd, mad, ...) are added like Java does.
func buff(stat buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		v := c.Value(key)
		switch key {
		case "level":
			v = c.Level
		case "one":
			v = 1
		}
		if v == 0 || c.Value("time") < 1 {
			return p, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
		}
		stats := append([]skills.StatValue{{Stat: stat, Value: v}}, skills.StandardStats(c.Values)...)
		p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: stats}
		return p, nil
	}
}

// party turns a self-buff Cast into a party buff inside the WZ lt/rb.
func party(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p, e := cast(c)
		if e != nil {
			return p, e
		}
		return skills.PartyPlan(c, p.Self, p)
	}
}

// teleportMastery (2111007, 2211007, 2311007) toggles TELEPORT_MASTERY x
// until recast (Java 2100000000 ms); Java charges y MP instead of mpCon. Teleporting with it
// on is a magic attack.
func teleportMastery(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("x") < 1 || c.Value("y") < 0 {
		return skills.CastPlan{}, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	return skills.CastPlan{MPCost: c.Value("y"), Toggle: true, Self: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "TELEPORT_MASTERY", Value: c.Value("x")}}}}, nil
}

func registerTeleportMastery(id int) {
	// The teleport hit arrives as a body attack (0x4E) and keeps magic damage.
	spec := magicSpec(id)
	spec.Kind |= combat.Body
	skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff | skills.KindAttack, Cast: teleportMastery, Attack: spec})
}

// Infinity starts at x, restores y% HP/MP immediately, and advances every
// four seconds (MapleCharacter.cancelEffect). GMS also supplies prop stance.
func infinity(c *skills.CastContext) (skills.CastPlan, error) {
	p, e := buff("INFINITY", "x")(c)
	if e != nil {
		return p, e
	}
	p.Self.Tick = 4 * time.Second
	p.Self.Stats = append(p.Self.Stats, skills.StatValue{Stat: "STANCE", Value: c.Value("prop")})
	p.Heal = &skills.Heal{HPPercent: c.Value("y"), MPPercent: c.Value("y")}
	return p, nil
}

// wandOrStaff reports a wand (137xxxx) or staff (138xxxx) in the weapon slot.
func wandOrStaff(c *skills.PassiveContext) bool {
	for _, e := range c.Character.Equipment {
		if e.Slot == -11 {
			return e.ItemID/10000 == 137 || e.ItemID/10000 == 138
		}
	}
	return false
}

// spellMastery (2100006, 2200006, 2300006) adds mastery and x magic attack
// with a wand or staff (Java PlayerStats passive mastery). A cleric's learned
// Holy Focus (2310008) supplies the mastery instead; the x attack stays.
func spellMastery(c *skills.PassiveContext) skills.Bonus {
	if !wandOrStaff(c) {
		return skills.Bonus{}
	}
	mastery := c.Values["mastery"]
	if c.Skill.ID == 2300006 {
		mastery = c.BasicMastery(2310008)
	}
	return skills.Bonus{Mastery: mastery, MATK: c.Values["x"]}
}

// criticalPassive (2110009, 2210009, 2310010) adds cr critical rate and
// criticaldamageMin minimum critical damage (WZ; Java leaves them unused).
func criticalPassive(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{Critical: c.Values["cr"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// highWisdom (2100007, 2200007, 2300007) adds intX INT.
func highWisdom(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{INT: c.Values["intX"]}
}

// elementAmplification (2110001, 2210001): MP costs become x% and damage y%
// (Java PlayerStats mpconPercent and dam_r).
func elementAmplification(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MPConsume: c.Values["x"] - 100, ElementAmp: c.Values["y"] - 100}
}

// arcaneAim (2120010, 2220010, 2320011): Java PlayerStats always counts the
// full x*y% damage and adds ignoreMobpdpR.
func arcaneAim(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{DamagePercent: c.Values["x"] * c.Values["y"], IgnoreDefense: c.Values["ignoreMobpdpR"]}
}

// elementalDrainFP (2110000) adds x DoT seconds and z DoT percent;
// elementalDrainIL (2210000) only z (Java PlayerStats).
func elementalDrainFP(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{DOTTime: c.Values["x"], DOTPercent: c.Values["z"]}
}

func elementalDrainIL(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{DOTPercent: c.Values["z"]}
}

// summonTER is the terR elemental resistance Java grants while Elquines or
// Ifrit is learned.
func summonTER(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{TER: c.Values["terR"]}
}

// buffMastery (2121009, 2221009, 2321010) adds madX magic attack; Java uses
// nothing else of it.
func buffMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MATK: c.Values["madX"]}
}

// passive registers a passive skill. A nil bonus means its effect lives
// elsewhere (attack pipeline procs) or waits for a Bonus field.
func passive(id int, bonus func(*skills.PassiveContext) skills.Bonus) {
	skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: bonus})
}
