package skills

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"sort"
	"time"
)

// TriggerContext is the input of OnAttack and OnDamaged. Fields may be added.
type TriggerContext struct {
	Skill     content.Skill
	Level     int
	Values    map[string]int
	Character domain.Character
	// Rows are this skill's active buff rows (empty when none).
	Rows []domain.SkillBuff
	// Attack is the applied attack for OnAttack; Damage the monster damage
	// taken for OnDamaged.
	Attack *combat.AttackEvent
	Damage int
	// Mob is the monster object that dealt the damage (OnDamaged); Mobs
	// are the living monsters in the owner's room, for counters that hit
	// several monsters around the owner.
	Mob  int32
	Mobs []TriggerMob
	// DefType is the hit packet's defType (1 = Power Guard block).
	DefType byte
	Now     time.Time
}

// TriggerMob is a living monster near a trigger.
type TriggerMob struct {
	ObjectID int32
	X, Y     int
	MaxHP    int64 // Template maximum HP (reflection caps such as maxHP/10).
}

// Attacker returns the monster that dealt the damage, if it is still alive.
func (c *TriggerContext) Attacker() (TriggerMob, bool) {
	for _, m := range c.Mobs {
		if m.ObjectID == c.Mob {
			return m, true
		}
	}
	return TriggerMob{}, false
}

// MobsInRange returns up to limit room monsters inside the skill's WZ lt/rb
// around the owner (mirror-symmetric; facing is not tracked).
func (c *TriggerContext) MobsInRange(limit int) []int32 {
	r, ok := c.Skill.RangeAt(c.Level)
	if !ok || limit < 1 {
		return nil
	}
	area := Rect{Left: r.LT[0], Top: r.LT[1], Right: r.RB[0], Bottom: r.RB[1]}
	var out []int32
	for _, m := range c.Mobs {
		if len(out) == limit {
			break
		}
		if InPartyRange(area, c.Character, domain.Character{X: m.X, Y: m.Y}) {
			out = append(out, m.ObjectID)
		}
	}
	return out
}

// Value returns a WZ level value, zero when absent.
func (c *TriggerContext) Value(key string) int { return c.Values[key] }

// Counter returns the server-only counter of this skill's rows.
func (c *TriggerContext) Counter() int {
	if len(c.Rows) == 0 {
		return 0
	}
	return c.Rows[0].Counter
}

// Stat returns the value of one of this skill's active rows.
func (c *TriggerContext) Stat(stat string) (int, bool) {
	for _, r := range c.Rows {
		if r.Stat == stat {
			return r.Value, true
		}
	}
	return 0, false
}

// BuffChange replaces a skill's buff rows (Buff) or removes them (Cancel).
// A nil *BuffChange from a trigger keeps the current state.
type BuffChange struct {
	Buff   *Buff
	Cancel bool
	// Cooldown starts the skill's cooldown with the change (Divine Shield
	// after it breaks).
	Cooldown time.Duration
	// HealHP/HealMP recover the owner (life drain on hit); CostMP drains MP
	// (a toggle's upkeep, Missile Tank), never below zero.
	HealHP, HealMP int
	CostMP         int
	// ReviveHPPercent (OnLethal only) cancels the death: the owner keeps
	// that percent of MaxHP; ReviveAtSpawn also moves them to the map's
	// spawn point 0.
	ReviveHPPercent int
	ReviveAtSpawn   bool
	// MobHits deal server-computed damage with this skill after the change
	// commits (counter-attacks on being hit, S0b DamageMobs). MobCast then
	// applies the skill's registered MobCast statuses to those monsters
	// (S0b CastOnMobs; the handler needs Attack.MobCast).
	MobHits []combat.ServerHit
	MobCast []int32
}

// SkillHits are a trigger's monster hits and casts for one skill.
type SkillHits struct {
	SkillID, Level int
	Hits           []combat.ServerHit
	Cast           []int32
}

// Ticks runs the OnTick of every learned skill whose period has passed since
// last[skillID] (updated in place; a new skill starts its period now).
func Ticks(c domain.Character, now time.Time, last map[int]time.Time) []domain.SkillBuffChange {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	var out []domain.SkillBuffChange
	for _, s := range c.Skills {
		if s.Level < 1 || !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			continue
		}
		h, ok := Lookup(s.ID)
		if !ok || h.OnTick == nil {
			continue
		}
		prev, seen := last[s.ID]
		if !seen {
			last[s.ID] = now
			continue
		}
		if now.Sub(prev) < h.TickEvery {
			continue
		}
		last[s.ID] = now
		def, err := content.SkillDefinition(root, s.ID)
		if err != nil || def.Disabled {
			continue
		}
		level := EffectiveLevel(c, s.ID, now)
		values, err := def.Values(level)
		if err != nil {
			continue
		}
		var rows []domain.SkillBuff
		for _, b := range c.SkillBuffs {
			if b.ID == s.ID && b.ExpiresAt.After(now) {
				rows = append(rows, b)
			}
		}
		if v, ok := changeFor(s, h.OnTick(&TriggerContext{Skill: def, Level: level, Values: values, Character: c, Rows: rows, Now: now})); ok {
			out = append(out, v)
		}
	}
	return out
}

// ChangeFor converts a BuffChange for a learned skill state.
func ChangeFor(s domain.SkillState, change *BuffChange) (domain.SkillBuffChange, bool) {
	return changeFor(s, change)
}

// Lethal runs OnLethal for the learned skills of c and for the skills of its
// active buffs (a party member holding Soul Stone without learning it, like
// Java's buff check); the first change that revives wins.
func Lethal(c domain.Character, now time.Time, damage int, mob int32) (domain.SkillBuffChange, *BuffChange, bool) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	states := make([]domain.SkillState, 0, len(c.Skills)+len(c.SkillBuffs))
	seen := map[int]bool{}
	for _, s := range c.Skills {
		if s.Level >= 1 && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			states = append(states, s)
			seen[s.ID] = true
		}
	}
	for _, b := range c.SkillBuffs {
		if !seen[b.ID] && b.ExpiresAt.After(now) && b.Level >= 1 {
			states = append(states, domain.SkillState{ID: b.ID, Level: b.Level})
			seen[b.ID] = true
		}
	}
	for _, s := range states {
		h, ok := Lookup(s.ID)
		if !ok || h.OnLethal == nil {
			continue
		}
		def, err := content.SkillDefinition(root, s.ID)
		if err != nil || def.Disabled {
			continue
		}
		level := s.Level // A received buff (Soul Stone) keeps the buff's level.
		if l := EffectiveLevel(c, s.ID, now); l > 0 {
			level = l
		}
		values, err := def.Values(min(level, def.TrueMax()))
		if err != nil {
			continue
		}
		var rows []domain.SkillBuff
		for _, b := range c.SkillBuffs {
			if b.ID == s.ID && b.ExpiresAt.After(now) {
				rows = append(rows, b)
			}
		}
		change := h.OnLethal(&TriggerContext{Skill: def, Level: level, Values: values, Character: c, Rows: rows, Damage: damage, Mob: mob, Now: now})
		if change == nil || change.ReviveHPPercent < 1 || change.ReviveHPPercent > 100 {
			continue
		}
		v, _ := changeFor(s, change)
		v.SkillID, v.Level = s.ID, s.Level
		return v, change, true
	}
	return domain.SkillBuffChange{}, nil, false
}

func changeFor(s domain.SkillState, change *BuffChange) (domain.SkillBuffChange, bool) {
	if change == nil || !change.Cancel && change.Buff == nil && change.Cooldown == 0 && change.HealHP == 0 && change.HealMP == 0 && change.CostMP == 0 {
		return domain.SkillBuffChange{}, false
	}
	v := domain.SkillBuffChange{SkillID: s.ID, Level: s.Level, Cancel: change.Cancel, Cooldown: change.Cooldown, HealHP: change.HealHP, HealMP: change.HealMP, CostMP: change.CostMP}
	if b := change.Buff; b != nil && !change.Cancel {
		v.Duration, v.Tick, v.Counter = b.Duration, b.Tick, b.Counter
		for _, st := range b.Stats {
			v.Stats = append(v.Stats, domain.SkillBuffStat{Stat: string(st.Stat), Value: st.Value})
		}
	}
	return v, true
}

// Triggers runs OnAttack (attack != nil) or OnDamaged for every learned,
// registered skill of c and returns the requested changes in skill order.
func Triggers(c domain.Character, now time.Time, attack *combat.AttackEvent, damage int, mob int32, mobs []TriggerMob, defType byte) ([]domain.SkillBuffChange, []SkillHits) {
	return triggers(c, now, attack, damage, mob, mobs, defType, false)
}

// ClientComboTriggers runs OnClientCombo for every learned registered skill.
func ClientComboTriggers(c domain.Character, now time.Time) []domain.SkillBuffChange {
	out, _ := triggers(c, now, nil, 0, 0, nil, 0, true)
	return out
}

func triggers(c domain.Character, now time.Time, attack *combat.AttackEvent, damage int, mob int32, mobs []TriggerMob, defType byte, combo bool) ([]domain.SkillBuffChange, []SkillHits) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	var out []domain.SkillBuffChange
	var hits []SkillHits
	for _, s := range c.Skills {
		if s.Level < 1 || !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			continue
		}
		h, ok := Lookup(s.ID)
		if !ok {
			continue
		}
		run := h.OnDamaged
		switch {
		case combo:
			run = h.OnClientCombo
		case attack != nil:
			run = h.OnAttack
		}
		if run == nil {
			continue
		}
		def, err := content.SkillDefinition(root, s.ID)
		if err != nil || def.Disabled {
			continue
		}
		level := EffectiveLevel(c, s.ID, now)
		values, err := def.Values(level)
		if err != nil {
			continue
		}
		var rows []domain.SkillBuff
		for _, b := range c.SkillBuffs {
			if b.ID == s.ID && b.ExpiresAt.After(now) {
				rows = append(rows, b)
			}
		}
		change := run(&TriggerContext{Skill: def, Level: level, Values: values, Character: c, Rows: rows, Attack: attack, Damage: damage, Mob: mob, Mobs: mobs, DefType: defType, Now: now})
		if change == nil {
			continue
		}
		if len(change.MobHits) > 0 || len(change.MobCast) > 0 {
			hits = append(hits, SkillHits{SkillID: s.ID, Level: s.Level, Hits: change.MobHits, Cast: change.MobCast})
		}
		if v, ok := changeFor(s, change); ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillID < out[j].SkillID })
	return out, hits
}

var buffApplier func(ownerID int64, change domain.SkillBuffChange)

// SetBuffApplier installs the world's out-of-cast buff path (one world per
// process).
func SetBuffApplier(f func(ownerID int64, change domain.SkillBuffChange)) { buffApplier = f }

// ApplyBuff changes a learned skill's buff rows on its owner outside a cast,
// e.g. the Beholder's Hex buff (1320009) from a summon's sub-skill. It is
// asynchronous: the world commits and publishes it like a trigger.
func ApplyBuff(ownerID int64, skillID, level int, change BuffChange) {
	if buffApplier == nil {
		return
	}
	v := domain.SkillBuffChange{SkillID: skillID, Level: level, Cancel: change.Cancel, Cooldown: change.Cooldown, HealHP: change.HealHP, HealMP: change.HealMP}
	if b := change.Buff; b != nil && !change.Cancel {
		v.Duration, v.Tick, v.Counter = b.Duration, b.Tick, b.Counter
		for _, st := range b.Stats {
			v.Stats = append(v.Stats, domain.SkillBuffStat{Stat: string(st.Stat), Value: st.Value})
		}
	}
	buffApplier(ownerID, v)
}
