package combat

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"math"
	"math/bits"
)

// nativeMaxLineDamage is the v145 per-line display ceiling.
const nativeMaxLineDamage = 999999

// MaxLineDamage is the per-line ceiling: the native one, or the long damage cap
// while MAPLEGO_LONG_DAMAGE is on.
func MaxLineDamage() int {
	if limit := protocol.LongDamageCap(); limit > 0 {
		return int(limit)
	}
	return nativeMaxLineDamage
}

// Weapon is the v145 client weapon type (CalcDamage sub_6A51AA, read by IDA
// from MapleStory145.exe; Open-Maple docs/server/combat/client-damage-formula.md §2.2):
// multiplier in hundredths and stat family.
type Weapon struct {
	Multiplier int // ×100
	Ranged     bool
	Projectile Ammo
	Luck       bool // LUK main stat (dagger/claw/katara/cane).
	DexMain    bool // DEX main stat (bow/crossbow/gun/dual bowgun).
	Magic      bool // Wand/staff.
	DexOnly    bool // Luck weapons whose secondary stat is DEX alone (claw, cane).
	Cannon     bool // max(STR,DEX) main, min(STR,DEX) secondary.
	Bare       bool // No weapon: the client multiplies by 1, not PAD.
	MasteryMin int  // Mastery floor constant ×100 (0.20 default, 0.15 ranged, 0.25 wand).
}

// WeaponFor follows GameConstants.getWeaponType (item/10000 % 100).
func WeaponFor(itemID int) Weapon {
	if itemID == 0 {
		return Weapon{Multiplier: 143, Bare: true, MasteryMin: 20}
	}
	switch itemID / 10000 % 100 {
	case 30, 31, 32:
		return Weapon{Multiplier: 120, MasteryMin: 20}
	case 33:
		return Weapon{Multiplier: 130, Luck: true, MasteryMin: 20}
	case 34:
		return Weapon{Multiplier: 130, Luck: true, MasteryMin: 20}
	case 35:
		return Weapon{Multiplier: 200, Ranged: true, MasteryMin: 20}
	case 36:
		return Weapon{Multiplier: 130, Luck: true, DexOnly: true, MasteryMin: 20}
	case 37, 38:
		return Weapon{Multiplier: 100, Magic: true, MasteryMin: 25}
	case 39:
		return Weapon{Multiplier: 143, Bare: true, MasteryMin: 20}
	case 40, 41, 42:
		return Weapon{Multiplier: 134, MasteryMin: 20}
	case 43, 44:
		return Weapon{Multiplier: 149, MasteryMin: 20}
	case 45:
		return Weapon{Multiplier: 130, Ranged: true, Projectile: AmmoArrow, DexMain: true, MasteryMin: 15}
	case 46:
		return Weapon{Multiplier: 135, Ranged: true, Projectile: AmmoCrossbowArrow, DexMain: true, MasteryMin: 15}
	case 47:
		return Weapon{Multiplier: 175, Ranged: true, Projectile: AmmoStar, Luck: true, DexOnly: true, MasteryMin: 15}
	case 48:
		return Weapon{Multiplier: 170, MasteryMin: 20}
	case 49:
		return Weapon{Multiplier: 150, Ranged: true, Projectile: AmmoBullet, DexMain: true, MasteryMin: 15}
	case 52:
		return Weapon{Multiplier: 130, Ranged: true, DexMain: true, MasteryMin: 15}
	case 53:
		return Weapon{Multiplier: 150, Ranged: true, Cannon: true, MasteryMin: 15}
	}
	return Weapon{Multiplier: 143, MasteryMin: 20}
}

// IsMageJob mirrors PlayerStats.calculateMaxBaseDamage's mage test.
func IsMageJob(job int) bool {
	return job >= 200 && job <= 232 || job >= 1200 && job <= 1212 || job >= 2200 && job <= 2218 || job >= 3200 && job <= 3212
}

// IsBeginnerJob is the client's beginner family test (job%1000 == 0, Evan
// 2001, Mercedes 2002, Phantom 2003, Citizen 3001): any weapon is scored as
// STR/DEX × 1.20.
func IsBeginnerJob(job int) bool {
	return job%1000 == 0 || job == 2001 || job == 2002 || job == 2003 || job == 3001
}

// MaxBaseDamage is the client's base maximum for one weapon:
// multiplier × (4×main + secondary) × attack / 100.
func MaxBaseDamage(p PlayerStats) int64 { return max(1, maxBaseScaled(p)/10000) }

// maxBaseScaled is MaxBaseDamage ×10000, kept exact for ceilings.
func maxBaseScaled(p PlayerStats) int64 {
	w := WeaponFor(p.WeaponID)
	main, sec, atk, mult := int64(p.STR), int64(p.DEX), int64(p.WATK), int64(w.Multiplier)
	switch {
	case IsMageJob(p.Job):
		main, sec, atk, mult = int64(p.INT), int64(p.LUK), int64(p.MATK), 100
	case IsBeginnerJob(p.Job):
		mult = 120
	case w.Cannon:
		main, sec = int64(max(p.STR, p.DEX)), int64(min(p.STR, p.DEX))
	case w.DexMain:
		main, sec = int64(p.DEX), int64(p.STR)
	case w.Luck:
		main, sec = int64(p.LUK), int64(p.DEX)
		if !w.DexOnly {
			sec += int64(p.STR)
		}
	}
	if w.Bare && !IsMageJob(p.Job) {
		atk = 1
	}
	// The client never scores below 1 base damage.
	return max(10000, mulDiv(mulDiv(mult, 4*main+sec, 1), atk, 1))
}

// mulDiv is v*m/d for non-negative inputs, saturating instead of overflowing:
// long damage lets the fixed-point bound outgrow int64.
func mulDiv(v, m, d int64) int64 {
	if v <= 0 || m <= 0 {
		return 0
	}
	high, low := bits.Mul64(uint64(v), uint64(m))
	if high >= uint64(d) {
		return math.MaxInt64
	}
	quotient, _ := bits.Div64(high, low, uint64(d))
	return int64(min(quotient, math.MaxInt64))
}

// DamageContext is the per-target input of a damage ceiling.
type DamageContext struct {
	Stats    PlayerStats
	Effect   Effect
	Mob      content.Mob
	Statuses MobStatuses
	Magic    bool
	HP       int64 // Target's current HP (Mob.HP is the template maximum).
	// Attacker is the authoritative caster (learned skills, buffs) so
	// passives can gate on it (Mortal Blow 3110001/3210001).
	Attacker domain.Character
}

// LineCeiling is the server upper bound for one non-critical damage line. It
// is an upper bound, not a roll: client damage above it is clipped. A
// critical line's bound is CritCeiling of it.
func LineCeiling(c DamageContext) int {
	_, ceiling := LineBounds(c)
	return ceiling
}

// LineBounds is the server's [floor, ceiling] for one non-critical damage
// line, following the v145 client's CalcDamage (Open-Maple
// docs/server/combat/client-damage-formula.md): base maximum × skill% × element × (100 − PDR×(1 − ignore%)) ×
// (100 + damage%). The floor is the client's mastery minimum, max × min(0.95,
// constant + mastery%), with the level penalty (−1% per level the monster is
// above the attacker) and without optional bonuses, so a legitimate roll is
// never below it. Fixed, immune and blocked lines have no floor (0).
func LineBounds(c DamageContext) (floor, ceiling int) {
	m := c.Mob
	if m.Invincible {
		return 0, 0
	}
	if m.HasFixedDamage || m.FixedDamage > 0 {
		if m.OnlyNormalAttack && c.Effect.SkillID != 0 {
			return 0, 0
		}
		return 0, max(0, m.FixedDamage)
	}
	if m.OnlyNormalAttack && c.Effect.SkillID != 0 {
		return 0, 0
	}
	if c.Statuses.BlocksDamage(c.Magic) {
		return 0, 1
	}
	if c.Effect.FixDamage > 0 {
		return 0, min(c.Effect.FixDamage, MaxLineDamage())
	}
	if MobEffectiveness(m, c.Effect.Element) == Immune {
		return 0, 1
	}
	stats := c.Stats
	if c.Magic {
		// Spell power (WZ mad) adds to magic attack; an upper bound, never a roll.
		stats.MATK += max(0, c.Effect.MAD)
	}
	w := WeaponFor(stats.WeaponID)
	// v is damage ×10000×100 (fixed point base × percent) until the end.
	v := mulDiv(maxBaseScaled(stats), int64(max(1, c.Effect.Damage)), 1)
	if !c.Magic && w.Magic && IsMageJob(stats.Job) {
		// A mage swinging a wand or staff: the client scores the magic
		// formula and keeps one fifth (PDamage, weapon type 37/38).
		v /= 5
	}
	switch MobEffectiveness(m, c.Effect.Element) {
	case Strong:
		v /= 2
	case Weak:
		v = mulDiv(v, 3, 2)
	}
	if c.Magic && c.Stats.ElementAmp > 0 {
		// Element Amplification multiplies every spell (MDamage step 6).
		v = mulDiv(v, int64(100+c.Stats.ElementAmp), 100)
	}
	rate := c.Statuses.DefenseRate(m.PDRate, false)
	if c.Magic {
		rate = c.Statuses.DefenseRate(m.MDRate, true)
	}
	// Ignored defense is a share of the monster's rate, not a subtraction.
	ignore := min(100, max(0, c.Stats.IgnoreDefense+c.Effect.IgnoreMobDefense))
	rate = max(0, min(100, rate))
	rate -= rate * ignore / 100
	defended := mulDiv(v, int64(100-rate), 100)
	mastery := min(95, w.MasteryMin+max(0, c.Stats.Mastery))
	low := mulDiv(defended, int64(mastery), 100)
	if under := m.Level - c.Stats.Level; under > 0 {
		low = mulDiv(low, int64(max(0, 100-under)), 100)
	}
	bonus := c.Stats.DamageRate
	if m.Boss {
		bonus += c.Stats.BossDamageRate
	}
	if c.Magic || !c.Stats.FullDefenseIgnore {
		v = defended
	}
	v = mulDiv(v, int64(100+max(bonus, 0)), 100)
	// Round the fractional upper bound upward and the floor downward once.
	line := v/1000000 + min(v%1000000, 1)
	if v == math.MaxInt64 {
		// Saturated: the true bound is unknown and only ever larger.
		line = int64(MaxLineDamage())
	}
	factor := c.Stats.PotentialDamageMultiplier
	if m.Boss {
		factor = c.Stats.PotentialBossDamageMultiplier
	}
	if factor != 0 {
		// Each revealed option is multiplicative in Java PlayerStats. Apply
		// its product before the single final ceil, without changing the
		// existing no-potential integer path or multiplying the floor policy.
		upper := math.Ceil(float64(v) / 1000000 * factor)
		if math.IsNaN(upper) || upper < 0 {
			return 0, 0
		}
		if upper >= float64(MaxLineDamage()) {
			line = int64(MaxLineDamage())
		} else {
			line = int64(upper)
		}
	}
	return int(min(low/1000000, int64(MaxLineDamage()))), int(min(line, int64(MaxLineDamage())))
}

// CritCeiling is the upper bound of a line the client flagged critical: the
// non-critical bound × (1 + cd/100) with cd ≤ 50 + criticaldamageMax (Sharp
// Eyes low byte and other crit-damage bonuses, folded into Stats.CritDamage).
// The client crits 5% of the time whatever the passives, so a flag never by
// itself rejects a line.
func CritCeiling(c DamageContext, ceiling int) int {
	if ceiling <= 1 {
		return ceiling
	}
	v := mulDiv(int64(ceiling), int64(150+max(0, c.Stats.CritDamage)), 1)
	return int(min(v/100+min(v%100, 1), int64(MaxLineDamage())))
}

// CritFloor is the lower bound of a flagged critical line: at least +20%.
func CritFloor(floor int) int { return floor * 120 / 100 }
