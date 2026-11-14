// Package combat is the S0b skill combat core: attack specifications, damage
// ceilings, hit-box admission, monster status rules, summons and field
// objects. It is pure logic; world owns rooms, locks and packets. The skill
// registry (internal/skills) imports this package, never the reverse.
package combat

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// AttackKind is the native input family.
type AttackKind uint8

const (
	Melee AttackKind = 1 << iota
	Shoot
	Magic
	Body
	Summoned
)

// F1 skeleton names used by registered job packages.
const (
	AttackMelee  = Melee
	AttackShoot  = Shoot
	AttackMagic  = Magic
	AttackBody   = Body
	AttackSummon = Summoned
)

func KindForOpcode(op uint16) AttackKind {
	switch op {
	case protocol.RecvMeleeAttack:
		return Melee
	case protocol.RecvShootAttack:
		return Shoot
	case protocol.RecvMagicAttack:
		return Magic
	case protocol.RecvBodyAttack:
		return Body
	}
	return 0
}

type Element uint8

const (
	Physical Element = iota
	Fire
	Ice
	Lightning
	Poison
	Holy
	Dark
)

// ElementFromWZ maps Skill.wz elemAttr letters.
func ElementFromWZ(s string) Element {
	switch s {
	case "f", "F":
		return Fire
	case "i", "I":
		return Ice
	case "l", "L":
		return Lightning
	case "s", "S":
		return Poison
	case "h", "H":
		return Holy
	case "d", "D":
		return Dark
	}
	return Physical
}

type Point struct{ X, Y int }

// Box is a WZ lt/rb rectangle relative to the actor, authored facing left.
type Box struct {
	LT, RB Point
	Set    bool
}

// At returns the absolute rectangle for an actor at origin; facing right
// mirrors x exactly like the native client.
func (b Box) At(origin Point, facingLeft bool) (lt, rb Point) {
	lt, rb = b.LT, b.RB
	if !facingLeft {
		lt.X, rb.X = -b.RB.X, -b.LT.X
	}
	return Point{origin.X + lt.X, origin.Y + lt.Y}, Point{origin.X + rb.X, origin.Y + rb.Y}
}

// Effect is the attack-relevant subset of a skill level (Java
// MapleStatEffect). FromSkill builds it from content.Skill.
type Effect struct {
	SkillID, Level        int
	Damage                int // Percent; 100 for a normal attack.
	FixDamage             int
	MobCount, AttackCount int
	MPCon, HPCon          int
	ForceCost             bool // MPCon came from forceCon; ordinary MP reducers never affect it.
	BulletCount           int  // Projectiles per attack line set.
	BulletConsume         int  // Overrides projectile consumption when >0.
	ItemCon, ItemConNo    int
	MoneyCon              int
	Cooltime, Time        time.Duration
	SubTime               time.Duration // WZ subTime (secondary status duration, e.g. 1201006 darkness).
	Prop                  int
	X, Y, Z               int
	DOT                   int // Percent of max base damage per tick.
	DOTTime, DOTInterval  time.Duration
	Range                 int
	Box                   Box
	Element               Element
	Mastery               int
	CriticalRate          int
	IgnoreMobDefense      int // ignoreMobpdpR percent.
	HPRatio               int // Percent of current HP paid (Java hpR = -x/100, set in Prepare).
	MAD                   int // WZ mad: spell power added to magic attack for magic ceilings.
	Summon                bool
	MonsterStatus         []MobStatusApply // Statuses this level inflicts on hit.
}

// Ammo is a projectile rule for Shoot attacks.
type Ammo uint8

const (
	AmmoAuto          Ammo = iota // From the equipped weapon.
	AmmoNone                      // Soul Arrow / skills without projectiles.
	AmmoArrow                     // 206xxxx bow arrows.
	AmmoCrossbowArrow             // 2061xxx.
	AmmoStar                      // 207xxxx throwing stars.
	AmmoBullet                    // 233xxxx bullets.
)

// AttackSpec is what a skill registers. Nil fields use the generic pipeline.
type AttackSpec struct {
	Kind   AttackKind // Allowed input families (bitmask).
	Effect Effect     // Static override; zero means "from content.Skill".
	Wire   protocol.AttackQuirk
	Ammo   Ammo
	// IgnoreRange skips lt/rb admission (teleport/whole-map skills); still
	// requires same map and live target.
	IgnoreRange bool
	// MaxDamage overrides the generic per-line ceiling.
	MaxDamage func(DamageContext) int
	// DamageRange gives the lowest and highest skill percent the client can
	// roll for a line, when it is not a single WZ value (Shadow Meso). Unlike
	// MaxDamage, the bound stays the generic formula, critical envelope included.
	DamageRange func(Effect) (low, high int)
	// Prepare runs before planning and may adjust ctx.Effect from the
	// caster's state (charge element, HP-ratio cost, resource requirement)
	// or reject the attack.
	Prepare func(*AttackContext) error
	// Validate runs after generic validation; return an error to reject.
	Validate func(*AttackContext) error
	// Drain returns HP/MP the attacker regains from dealt damage (Dragon
	// Judgement lifesteal, MP Eater); bounded by damage and MaxHP/MaxMP and
	// applied in the same settlement.
	Drain func(ctx *AttackContext, dealt int64) (hp, mp int)
	// EruptAreas detonates the attacker's own areas of these skills when the
	// attack lands (Fire Demon / Inferno on Poison Mist).
	EruptAreas []int
	// AfterHit runs after the attack is applied (S0a applies self effects,
	// e.g. Java effect.applyTo for Assassinate/Owl Spirit). Outside locks.
	AfterHit func(AttackEvent)
	// ConsumeMesos retires the ground mesos the attack names (Meso
	// Explosion) before planning; AttackContext.Mesos holds their amounts.
	ConsumeMesos bool
	// MobCast is set for skills cast on monsters without damage
	// (Threaten, Hypnotize, Dispel); S0a's cast path calls the world.
	MobCast *MobCastSpec
	// TeleportKey sends a fresh CUserLocal::OnRandomTeleportKey (0x164) after
	// the cast (Dragon Blink 22141004). The v145 client moves itself before
	// casting: key&3 picks one of four horizontal map bands (0x877EEA), so the
	// server only re-rolls the key; Java's portal-ID teleport does not apply.
	TeleportKey bool
	// OnHit adds statuses beyond Effect.MonsterStatus.
	OnHit []MobStatusApply
	// Remap substitutes another registered attack skill for this input
	// (Java rangedAttack: Heavy Machine Gun 35111004 inside the Missile Tank
	// 35121005 becomes 35121013); 0 keeps the skill.
	Remap func(c domain.Character, now time.Time) int
	// NoCooldown: the attack never starts the WZ cooltime (mech mode skills
	// whose cooltime belongs to the mode toggle).
	NoCooldown bool
	// LevelFrom names the learned skill whose level authorizes this hidden
	// linked attack (Java GameConstants.getLinkedAttackSkill), e.g.
	// 31001006 -> 31000004. Zero means the skill's own learned level.
	LevelFrom int
	// Summon is set for skills that create a summon.
	Summon *SummonSpec
	// Field is set for skills that place a field object (mist, door).
	Field *FieldSpec
}

// Allows reports whether an input family may carry this skill. A nil spec
// is the ordinary attack; a registered spec with Kind 0 (cast-only summon,
// field object or monster cast) never accepts attack packets.
func (s *AttackSpec) Allows(k AttackKind) bool {
	if s == nil {
		return k == Melee || k == Shoot || k == Magic
	}
	return s.Kind&k != 0
}

// PlayerStats are the effective attacker numbers S0a computes (passives,
// buffs, equipment). BaseStats provides an equipment-only fallback.
type PlayerStats struct {
	ResourceCosts                                            content.ResourceCostModifiers // Prepared outside the room lock; no WZ reads in PlanAttack.
	Job, Level                                               int
	STR, DEX, INT, LUK                                       int
	WATK, MATK                                               int
	WeaponID, SecondaryWeaponID                              int
	Accuracy                                                 int
	Mastery                                                  int     // Percent.
	CritRate, CritDamage                                     int     // Percent chance / extra percent on a critical.
	DamageRate, BossDamageRate                               int     // Percent bonus (0 = none).
	IgnoreDefense                                            int     // Percent of monster PDRate/MDRate ignored.
	ElementAmp                                               int     // Percent bonus for magic element amplification.
	ShadowPartner                                            int     // Percent of an extra mirrored line set (0 = none).
	WDEF, MDEF, Avoid, Speed, Jump                           int     // Effective view; contact damage/movement do not yet consume these server-side.
	DodgeRate, ASR, TER, CritDamageMin                       int     // Effective view; no new probabilistic gate is implied.
	PotentialAvoidPercent                                    int     // Native incEVAr source, not DodgeRate; full base-EVA percentage application remains pending.
	PotentialDamageMultiplier, PotentialBossDamageMultiplier float64 // zero means identity; each option multiplies once.
}

// StatsProvider is implemented by S0a (skills.Stats): passives, buffs and
// equipment folded into one effective PlayerStats.
type StatsProvider interface {
	PlayerStats(c domain.Character, now time.Time) PlayerStats
}

// CheckedStatsProvider allows authoritative sources to report missing or
// unsupported data rather than silently returning an equipment-only fallback.
// Existing providers remain compatible with StatsProvider.
type CheckedStatsProvider interface {
	PlayerStatsChecked(c domain.Character, now time.Time) (PlayerStats, error)
}

// StatsFunc adapts a function to StatsProvider.
type StatsFunc func(domain.Character, time.Time) PlayerStats

func (f StatsFunc) PlayerStats(c domain.Character, now time.Time) PlayerStats { return f(c, now) }
