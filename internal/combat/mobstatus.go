package combat

import (
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

type MobStat = protocol.MobStat

// Effectiveness of an element against a monster (WZ elemAttr digit).
type Effectiveness uint8

const (
	Normal Effectiveness = iota
	Immune
	Strong
	Weak
)

// MobEffectiveness parses elemAttr like "F2I3S1" (1 immune, 2 strong, 3 weak).
func MobEffectiveness(m content.Mob, e Element) Effectiveness {
	if e == Physical {
		return Normal
	}
	letter := map[Element]byte{Fire: 'F', Ice: 'I', Lightning: 'L', Poison: 'S', Holy: 'H', Dark: 'D'}[e]
	s := strings.ToUpper(m.ElemAttr)
	for i := 0; i+1 < len(s); i += 2 {
		if s[i] == letter {
			switch s[i+1] {
			case '1':
				return Immune
			case '2':
				return Strong
			case '3':
				return Weak
			}
		}
	}
	return Normal
}

// MobStatusApply describes one status a skill inflicts on hit.
type MobStatusApply struct {
	Stat MobStat
	// Value is literal unless ValueFrom names an effect field: "x", "y", "z".
	Value     int
	ValueFrom string
	Duration  time.Duration // Zero uses Effect.Time (or SubTime).
	SubTime   bool          // Use Effect.SubTime (WZ subTime) as the duration.
	// HalfDurationOnBoss keeps the status applicable while shortening it.
	HalfDurationOnBoss bool
	Prop               int // Zero uses Effect.Prop; zero effect prop means always.
	// Poison makes the status a damage-over-time: value is the per-tick
	// damage from Effect.DOT percent of max base damage (Java applyStatus).
	Poison bool
	// TickFrom picks the DoT damage source: "" = Effect.DOT% of max base
	// damage (or the literal value); "damage" = Effect.Damage% of max base
	// (Assassinate/Ninja Ambush); "mobhp50" = monster max HP / 50 (Shadow Web).
	TickFrom string
	// Explode deals the tick damage once at expiry instead of periodically
	// (Monster Bomb). Like every DoT it never kills.
	Explode bool
	// SourceSkill identifies a passive that can proc from several attack skills.
	SourceSkill int
	StackLimit  int
	// DOTInterval uses the passive's interval when a different skill hits.
	DOTInterval time.Duration
	// DOTPercent overrides Effect.DOT when a passive/potential DoT procs from
	// another attack skill (e.g. 90001003 Poison dot=40%).
	DOTPercent int
}

// ActiveStatus is one monster status held in world room memory.
type ActiveStatus struct {
	Stat    MobStat
	Value   int32
	SkillID int32
	OwnerID int64 // Character credited for DoT damage.
	// Owner session identity, so a reconnect never inherits DoT credit.
	OwnerSession    string
	OwnerGeneration int64
	ExpiresAt       time.Time
	// DoT: Tick damage every Interval; never reduces HP below 1.
	TickDamage int64
	Interval   time.Duration
	NextTick   time.Time
	Explode    bool
	StackLimit int
	Stacks     int
}

// MobStatuses is the per-mob active status set.
type MobStatuses map[MobStat]*ActiveStatus

func (m MobStatuses) Values() []protocol.MobStatValue {
	out := make([]protocol.MobStatValue, 0, len(m))
	for _, s := range m {
		out = append(out, protocol.MobStatValue{Stat: s.Stat, Value: s.Value, Reason: s.SkillID, ExpiresAt: s.ExpiresAt})
	}
	return out
}

// Has reports an active status.
func (m MobStatuses) Has(s MobStat) bool { return m[s] != nil }

// CanInflict follows Java MapleMonster.applyStatus immunities: element
// immunity/strength, boss stun/control immunity, PImmune/MImmune ignored here
// (they block damage, not status).
func CanInflict(m content.Mob, s MobStat, e Element) bool {
	if (!protocol.MobStatOrdinary(s) && s != MobBurned) || m.Invincible {
		return false
	}
	switch MobEffectiveness(m, e) {
	case Immune, Strong:
		return false
	}
	if s == MobPoison || s == MobVenom || s == MobBurned {
		if p := MobEffectiveness(m, Poison); p == Immune || p == Strong {
			return false
		}
	}
	if m.Boss {
		switch s {
		case MobSpeed, MobAmbush, MobPAD, MobPoison, MobBurned, MobDarkness, MobMagicCrash, MobPDR, MobMDR:
		default:
			return false
		}
	}
	return true
}

// Resolve turns a registered apply into an ActiveStatus. roll(prop) reports
// the server-side success roll. maxBase is the attacker's max base damage.
func Resolve(a MobStatusApply, eff Effect, ownerID int64, maxBase int64, now time.Time, roll func(int) bool) (ActiveStatus, bool) {
	return ResolveOn(a, eff, ownerID, maxBase, content.Mob{}, now, roll)
}

// ResolveOn is Resolve with the target template (for monster-HP based ticks).
func ResolveOn(a MobStatusApply, eff Effect, ownerID int64, maxBase int64, mob content.Mob, now time.Time, roll func(int) bool) (ActiveStatus, bool) {
	prop := a.Prop
	if prop == 0 {
		prop = eff.Prop
	}
	if prop > 0 && prop < 100 && (roll == nil || !roll(prop)) {
		return ActiveStatus{}, false
	}
	d := a.Duration
	if d == 0 && a.SubTime {
		d = eff.SubTime
	}
	if d == 0 {
		d = eff.Time
	}
	if a.Poison && a.Stat != MobBurned && eff.DOTTime > d {
		d = eff.DOTTime
	}
	if mob.Boss && a.HalfDurationOnBoss {
		d /= 2
	}
	if d <= 0 || d > 10*time.Minute {
		return ActiveStatus{}, false
	}
	v := a.Value
	switch a.ValueFrom {
	case "x":
		v = eff.X
	case "y":
		v = eff.Y
	case "z":
		v = eff.Z
	}
	if v == 0 && a.ValueFrom == "" {
		// Java sends 1 for flag statuses (stun, freeze, seal...); the native
		// client treats a zero value as an absent stat.
		v = 1
	}
	if a.Stat == MobDazzle {
		// The v148 CMob owner check (0x5F7EF4) compares this value to the
		// local user's character ID before dispatching mob-on-mob attacks.
		if ownerID <= 0 || ownerID > 1<<31-1 {
			return ActiveStatus{}, false
		}
		v = int(ownerID)
	}
	st := ActiveStatus{Stat: a.Stat, Value: int32(v), SkillID: int32(eff.SkillID), OwnerID: ownerID, ExpiresAt: now.Add(d)}
	if a.SourceSkill != 0 {
		st.SkillID = int32(a.SourceSkill)
	}
	if a.Poison || a.Explode {
		dot := int64(eff.DOT)
		if a.DOTPercent > 0 {
			dot = int64(a.DOTPercent)
		}
		switch {
		case a.TickFrom == "damage":
			st.TickDamage = max(1, int64(eff.Damage)*maxBase/100)
		case a.TickFrom == "mobhp50":
			st.TickDamage = max(1, (mob.HP+49)/50)
		case a.Stat == MobBurned:
			st.TickDamage = int64(max(v, 0))
		case dot <= 0:
			st.TickDamage = int64(max(v, 0))
		default:
			st.TickDamage = max(1, dot*maxBase/100)
		}
		st.Interval = a.DOTInterval
		if st.Interval <= 0 {
			st.Interval = eff.DOTInterval
		}
		if st.Interval <= 0 {
			st.Interval = time.Second
		}
		st.NextTick = now.Add(st.Interval)
		if a.Explode {
			st.NextTick, st.Interval, st.Explode = st.ExpiresAt, 0, true
		}
		if a.Poison {
			st.StackLimit = min(16, a.StackLimit)
			st.Stacks = 1
			st.Value = int32(min(st.TickDamage, 0x7fffffff))
		}
	}
	return st, true
}

// Apply keeps an unexpired DoT from the same skill, unless the skill supports
// stacking and the new proc came from the same character and session.
func (m MobStatuses) Apply(s ActiveStatus, now time.Time) bool {
	if old := m[s.Stat]; old != nil && old.TickDamage > 0 && old.SkillID == s.SkillID && old.ExpiresAt.After(now) {
		if s.StackLimit < 2 || old.OwnerID != s.OwnerID || old.OwnerSession != s.OwnerSession || old.OwnerGeneration != s.OwnerGeneration {
			return false
		}
		s.Stacks = min(old.Stacks+1, s.StackLimit)
		s.TickDamage = min(s.TickDamage*int64(s.Stacks), 0x7fffffff)
		s.Value = int32(s.TickDamage)
		s.NextTick = old.NextTick
	}
	c := s
	m[s.Stat] = &c
	return true
}

// Expire removes ended statuses and returns them.
func (m MobStatuses) Expire(now time.Time) []MobStat {
	var out []MobStat
	for k, s := range m {
		if !now.Before(s.ExpiresAt) {
			out = append(out, k)
			delete(m, k)
		}
	}
	return out
}

// DoTHit is one tick of damage credited to an owner.
type DoTHit struct {
	OwnerID         int64
	OwnerSession    string
	OwnerGeneration int64
	SkillID         int32
	Damage          int64
}

func (s *ActiveStatus) doTHit(damage int64) DoTHit {
	return DoTHit{OwnerID: s.OwnerID, OwnerSession: s.OwnerSession, OwnerGeneration: s.OwnerGeneration,
		SkillID: s.SkillID, Damage: damage}
}

// Tick applies due DoT ticks against hp, never reducing it below 1.
func (m MobStatuses) Tick(now time.Time, hp int64) (int64, []DoTHit) {
	var hits []DoTHit
	for _, s := range m {
		if s.Explode {
			// Java doPoison (MONSTER_BOMB): a blast that would reach the
			// remaining HP is skipped entirely, never clipped to 1 HP.
			if s.TickDamage > 0 && !now.Before(s.ExpiresAt) && s.TickDamage < hp {
				hp -= s.TickDamage
				hits = append(hits, s.doTHit(s.TickDamage))
			}
			if !now.Before(s.ExpiresAt) {
				s.TickDamage = 0
			}
			continue
		}
		for s.TickDamage > 0 && s.Interval > 0 && !now.Before(s.NextTick) && !s.NextTick.After(s.ExpiresAt) && hp > 1 {
			d := min(s.TickDamage, hp-1)
			hp -= d
			hits = append(hits, s.doTHit(d))
			s.NextTick = s.NextTick.Add(s.Interval)
		}
	}
	return hp, hits
}

// DefenseRate returns PDRate/MDRate after PDR/MDR statuses (value is a
// percent delta, usually negative for Threaten/Armor Crash).
func (m MobStatuses) DefenseRate(base int, magic bool) int {
	s := MobPDR
	if magic {
		s = MobMDR
	}
	if st := m[s]; st != nil {
		base += int(st.Value)
	}
	return max(0, min(100, base))
}

// BlocksDamage reports PImmune/MImmune/HardSkin-style damage immunity.
func (m MobStatuses) BlocksDamage(magic bool) bool {
	if magic {
		return m.Has(protocol.MobMImmune)
	}
	return m.Has(protocol.MobPImmune)
}
