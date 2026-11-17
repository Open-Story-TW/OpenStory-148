// Package skills is the skill registry (freeze point F1 of
// Open-Maple docs/server/SKILLS-PARALLEL-PLAN.md). Every castable, attacking or passive skill
// is a Handler registered from the init function of its job package
// (internal/skills/<job>); internal/skills/all imports every job package.
// An unregistered skill is always rejected.
//
// Handlers are pure: they turn the authoritative caster state and the WZ
// level values into a plan. The shared core (persistence, world) validates and
// applies the plan transactionally and publishes packets, so job packages
// never touch shared files, SQL or the buff mask encoding.
package skills

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies a skill for listing, QA and acceptance. A skill may have
// several kinds (a party buff that also heals).
type Kind uint16

const (
	KindPassive Kind = 1 << iota
	KindAttack
	KindBuff
	KindPartyBuff
	KindSummon
	KindMobStatus
	KindHeal
	KindFieldObject
	KindMorph
	KindSpecial
)

// Handler is one registered skill. Any of Cast, Attack and Passive may be nil.
type Handler struct {
	ID   int
	Kind Kind
	// LevelFrom names the learned skill whose level a linked skill is cast
	// at (33101008 uses 33101004's level); zero means the skill itself.
	LevelFrom int
	// AllowDisabledCast accepts an already learned native activation whose WZ
	// disables ordinary use. Allocation still follows the raw WZ flag.
	AllowDisabledCast bool
	// Cast resolves a RecvSkillCast activation (buff, party buff, heal,
	// summon, field object). Nil means the skill cannot be cast that way.
	Cast func(*CastContext) (CastPlan, error)
	// Attack is the attack specification consumed by the S0b attack
	// pipeline; nil means the skill is not an attack.
	Attack *combat.AttackSpec
	// Passive contributes always-on bonuses to the effective stats.
	Passive func(*PassiveContext) Bonus
	// OnAttack lets a learned skill react to its owner's applied attacks
	// (combo orbs, energy charge, hit-count buffs, Final Cut); ctx.Attack
	// names the attacking skill. OnDamaged reacts to monster damage taken.
	// Both run outside the attack transaction and only change buff rows.
	OnAttack  func(*TriggerContext) *BuffChange
	OnDamaged func(*TriggerContext) *BuffChange
	// OnClientCombo runs for each learned skill when the client reports an
	// Aran combo hit (RecvAranCombo 0xEA, Java PlayerHandler.AranCombo);
	// ctx.Attack is nil. A CounterPacket skill answers it with its Counter.
	OnClientCombo func(*TriggerContext) *BuffChange
	// OnTick runs every TickEvery for a learned skill while its owner is
	// alive in the field (periodic passives: Demon Fury or HP/MP recovery).
	OnTick    func(*TriggerContext) *BuffChange
	TickEvery time.Duration
	// OnLethal runs, for learned skills, when a monster hit would kill the
	// owner; a BuffChange with ReviveHPPercent saves them (Soul Stone).
	OnLethal func(*TriggerContext) *BuffChange
	// CounterPacket answers RecvAranCombo with this skill's server-only
	// Counter (Aran combo 0x153, Java testCombo); it is sent only as that
	// reply. A change that only moves the Counter never re-sends the buff.
	CounterPacket func(counter int) []byte
	// OnEnd runs after this skill's buff rows end on a character (expiry,
	// cancel, toggle, replacement by another skill or death) and none of its
	// rows remain. It runs outside the commit; use ApplyBuff to restore
	// another skill (35121013 ending puts Missile Tank 35121005 back).
	OnEnd func(*EndContext)
	// DropOnEnter removes this skill's rows from a holder entering a field
	// (map change or login) when it returns true: Blaze Wizard auras leave
	// recipients that are not 3200-3212 (Java MapleMap AURA cancel).
	DropOnEnter func(holder domain.Character) bool

	pkg string
}

// Package is the job package that registered the handler, e.g. "warrior".
func (h Handler) Package() string { return h.pkg }

var registry = struct {
	sync.RWMutex
	byID map[int]Handler
}{byID: map[int]Handler{}}

// Register adds h. It panics on a duplicate or invalid handler, so a broken
// registration fails at process start and in every test.
func Register(h Handler) {
	if h.ID < 1 || h.ID > 99999999 || h.Kind == 0 {
		panic(fmt.Sprintf("skills: invalid handler %d", h.ID))
	}
	if h.OnTick != nil && (h.TickEvery < time.Second || h.TickEvery > time.Hour) {
		panic(fmt.Sprintf("skills: handler %d has an invalid TickEvery", h.ID))
	}
	if h.Cast == nil && h.Attack == nil && h.Passive == nil && h.OnAttack == nil && h.OnDamaged == nil && h.OnClientCombo == nil && h.OnTick == nil && h.OnLethal == nil && h.OnEnd == nil && h.DropOnEnter == nil && h.Kind&KindPassive == 0 {
		panic(fmt.Sprintf("skills: handler %d has no behaviour", h.ID))
	}
	h.pkg = callerPackage()
	registry.Lock()
	defer registry.Unlock()
	if prior, dup := registry.byID[h.ID]; dup {
		panic(fmt.Sprintf("skills: %d registered by %s and %s", h.ID, prior.pkg, h.pkg))
	}
	registry.byID[h.ID] = h
}

// Lookup returns the handler for id.
func Lookup(id int) (Handler, bool) {
	registry.RLock()
	defer registry.RUnlock()
	h, ok := registry.byID[id]
	return h, ok
}

// IDs returns every registered skill ID in ascending order.
func IDs() []int {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]int, 0, len(registry.byID))
	for id := range registry.byID {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// callerPackage returns the last path element of the package calling Register.
func callerPackage() string {
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return ""
	}
	name := runtime.FuncForPC(pc).Name() // e.g. maple-go-server/internal/skills/warrior.init.0
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "."); i >= 0 {
		name = name[:i]
	}
	return name
}

// CastContext is the authoritative input of a Cast. Fields may be added.
// Caster carries the current buffs (Caster.SkillBuffs) and cooldowns.
type CastContext struct {
	Skill  content.Skill
	Level  int
	Values map[string]int // Skill.Values(Level)
	Caster domain.Character
	Now    time.Time
}

// Value returns a WZ level value, zero when absent.
func (c *CastContext) Value(key string) int { return c.Values[key] }

// Active reports whether the caster currently has a buff of skill id.
func (c *CastContext) Active(id int) bool { return ActiveBuff(c.Caster, id, c.Now) }

// ActiveBuff reports whether c currently has a buff of skill id.
func ActiveBuff(c domain.Character, id int, now time.Time) bool {
	for _, b := range c.SkillBuffs {
		if b.ID == id && b.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// ActiveStat returns the value of an active buff stat on c.
func ActiveStat(c domain.Character, stat buffstat.Stat, now time.Time) (int, bool) {
	for _, b := range c.SkillBuffs {
		if b.Stat == string(stat) && b.ExpiresAt.After(now) {
			return b.Value, true
		}
	}
	return 0, false
}

// StatValue is one buff stat and its value. Zero and negative values are
// valid (BOOSTER -2, SPIRIT_CLAW 0).
type StatValue struct {
	Stat  buffstat.Stat
	Value int
}

// Permanent is the longest buff the client accepts (I32 milliseconds; Java
// uses it for toggles and hit-count buffs). It still ends on death, cancel
// or expiry of the session's buffs.
const Permanent = 2100000000 * time.Millisecond

// Buff is a temporary effect of one skill; it may carry several stats
// (Hyper Body is MAXHP and MAXMP). A party plan may use a Buff without
// stats to target the party for Heal, Dispel or ResetCooldowns only.
type Buff struct {
	Duration time.Duration
	Stats    []StatValue
	// Tick is the period of a recurring effect (Recovery heals every 5s).
	Tick time.Duration
	// Counter is server-only state stored with the rows and never sent to
	// the client (Owl Spirit's remaining hits while the value is the damage
	// bonus). Triggers read it back through TriggerContext.Counter.
	Counter int
}

// Rect is a range relative to the caster, from WZ lt/rb; the core mirrors it
// when the caster faces right.
type Rect struct{ Left, Top, Right, Bottom int }

// Heal restores flat and percent-of-maximum HP/MP.
type Heal struct {
	HP, MP               int
	HPPercent, MPPercent int
}

// ItemCost is an inventory item consumed by the cast (WZ itemCon/itemConNo).
type ItemCost struct{ ItemID, Quantity int }

// CastPlan is what a Cast asks the core to do. The core rejects a plan whose
// costs, durations or cooldown are out of range. A plan with neither Self
// nor Party only pays its costs and shows the cast (Double Jump, Teleport).
type CastPlan struct {
	MPCost, HPCost int
	Items          []ItemCost
	Cooldown       time.Duration
	// Self is applied to the caster.
	Self *Buff
	// Party is applied to every living party member on the caster's map
	// inside Area, caster included. A nil Area means the skill's WZ lt/rb;
	// the core rejects a Party plan when the skill has no range either.
	Party *Buff
	Area  *Rect
	// Heal, Dispel and ResetCooldowns apply to the caster, or to every
	// party recipient for a Party plan. Dispel removes the abnormal
	// statuses (buffstat.Abnormal); ResetCooldowns clears skill cooldowns.
	Heal           *Heal
	Dispel         bool
	ResetCooldowns bool
	// Toggle makes a recast while the buff is active cancel it at no cost.
	Toggle bool
	// Revive makes a Party plan target dead party members in range (not
	// the living ones) and restores them to full HP (Java Resurrection).
	Revive bool
	// Replace removes the recipients' buffs of these skills before the new
	// rows are applied (auras cancel each other and Body Boost).
	Replace []int
	// Retime sets the remaining time of the caster's active buffs of other
	// skills (Body Boost shortens the running aura).
	Retime []SkillDuration
	// MoveTo warps the caster after the cast (999999999: the map's return
	// map), like WZ moveTo.
	MoveTo int
	// Changes replace or cancel the caster's buff rows of other learned
	// skills with the cast (Combo Drain adds to ARAN_COMBO's counter now).
	Changes []SkillChange
	// Consume lowers the caster's active buff stats (another skill's combo
	// orbs): the cast fails unless each stat is active with at least that
	// value; the row keeps its expiry.
	Consume []StatValue
	// ApplyAs stores Self under another skill's ID (at ApplyLevel, default
	// the cast level): 35111004 cast in Missile Tank becomes 35121013, like
	// Java MapleStatEffect. Toggle then checks that skill's rows.
	ApplyAs, ApplyLevel int
}

// EndContext is the input of OnEnd. Character is the owner after the change.
type EndContext struct {
	SkillID, Level int
	Character      domain.Character
	Now            time.Time
}

// SkillChange is a BuffChange for another learned skill.
type SkillChange struct {
	SkillID int
	Change  BuffChange
}

// SkillDuration is a remaining time for one skill's buff rows.
type SkillDuration struct {
	SkillID  int
	Duration time.Duration
}

// SkillDamage is a damage percent bonus for one skill.
type SkillDamage struct{ Skill, Percent int }

// PassiveContext is the input of a Passive. Fields may be added.
// Character carries the current buffs (Character.SkillBuffs); Now tells which
// of them are active (a bonus that only applies while a buff runs).
type PassiveContext struct {
	Skill     content.Skill
	Level     int
	Values    map[string]int
	Character domain.Character
	Now       time.Time
}

// Active reports whether the character currently has a buff of skill id.
func (c *PassiveContext) Active(id int) bool { return ActiveBuff(c.Character, id, c.Now) }

// Bonus is an additive contribution to the effective stats (Java
// PlayerStats). Percent fields are percentage points. Fields may be added.
type Bonus struct {
	MaxHP, MaxMP               int
	MaxHPPercent, MaxMPPercent int
	STR, DEX, INT, LUK         int
	STRPercent, DEXPercent     int
	INTPercent, LUKPercent     int
	WATK, MATK, WDEF, MDEF     int
	WATKPercent, MATKPercent   int
	WDEFPercent, MDEFPercent   int
	ACC, AVOID, Speed, Jump    int
	ACCPercent, AVOIDPercent   int
	// AvoidRate is the chance to dodge outright (Java dodgeChance).
	AvoidRate                int
	Critical, CriticalDamage int // Rate and maximum critical damage bonus.
	CriticalDamageMin        int
	Mastery                  int
	DamagePercent            int // damR on every attack.
	BossDamagePercent        int
	IgnoreDefense            int  // ignoreMobpdpR.
	FullDefenseIgnore        bool // A proc can ignore the target's entire physical defense rate.
	// SkillDamage adds damage percent to specific skills (passive damR
	// boosts). A fixed array keeps Bonus comparable.
	SkillDamage    [4]SkillDamage
	ElementAmp     int // Magic elemental amplification percent.
	MPConsume      int // Percent change of MP costs (negative saves MP).
	DOTPercent     int
	DOTTime        int // Extra DoT seconds.
	TER, ASR       int // Elemental and abnormal status resistance percent.
	HPRecovery     int // Flat HP recovered per regeneration tick.
	MPRecovery     int
	HPRecoveryRate int // Percent bonus on recovery.
	MPRecoveryRate int
	HPOnHit        int // HP recovered when attacking (Java hp per hit).
	PotionPercent  int // Potion effect bonus.
	MesoRate       int // Meso drop rate percent.
	Range          int // Extra shoot range.
	EXP            int // Extra experience percent.
	DamageReduce   int // Percent of received damage absorbed.
}
