package combat

import (
	"fmt"
	"sync"
	"time"

	"maple-go-server/internal/domain"
)

// Attack specs are registered through skills.Handler.Attack (S0a registry);
// the world looks them up there. This file only holds the stats hook.
var (
	statsMu     sync.RWMutex
	statsSource StatsProvider
)

// SetStatsProvider installs S0a's effective-stat calculator; nil restores
// the BaseStats fallback.
func SetStatsProvider(p StatsProvider) {
	statsMu.Lock()
	defer statsMu.Unlock()
	statsSource = p
}

// AttackStats returns the effective attacker stats.
func AttackStats(c domain.Character, now time.Time) (PlayerStats, error) {
	statsMu.RLock()
	p := statsSource
	statsMu.RUnlock()
	if p == nil {
		return BaseStats(c, now)
	}
	var s PlayerStats
	if checked, ok := p.(CheckedStatsProvider); ok {
		var err error
		s, err = checked.PlayerStatsChecked(c, now)
		if err != nil {
			return s, err
		}
	} else {
		s = p.PlayerStats(c, now)
	}
	if s.STR < 0 || s.DEX < 0 || s.INT < 0 || s.LUK < 0 || s.WATK < 0 || s.MATK < 0 {
		return s, fmt.Errorf("invalid attack stats")
	}
	return s, nil
}

var normalAttack struct {
	sync.RWMutex
	spec *AttackSpec
}

// SetNormalAttack installs the ordinary-attack (skill 0) spec, e.g. a
// Prepare that sets NoAmmo while Shadow Stars/Soul Arrow is active. Nil
// restores the plain ordinary attack.
func SetNormalAttack(s *AttackSpec) {
	normalAttack.Lock()
	normalAttack.spec = s
	normalAttack.Unlock()
}

// NormalAttack returns the installed ordinary-attack spec (nil = plain).
func NormalAttack() *AttackSpec {
	normalAttack.RLock()
	defer normalAttack.RUnlock()
	return normalAttack.spec
}

// Global attack modifiers are passives that apply to every attack of a job
// (Mortal Blow's ceiling on low-HP targets and its HP regain). Job packages
// register them from init; each must check the attacker itself.
var modifiers struct {
	sync.RWMutex
	ceilings []func(DamageContext, int) int
	drains   []func(*AttackContext, int64) (hp, mp int)
}

// ModifyCeiling registers f(dc, ceiling) -> new ceiling for every attack line.
func ModifyCeiling(f func(DamageContext, int) int) {
	modifiers.Lock()
	modifiers.ceilings = append(modifiers.ceilings, f)
	modifiers.Unlock()
}

// ModifyDrain registers an HP/MP regain applied to every attack, bounded like
// AttackSpec.Drain. ctx.Targets carries each target's HP and template.
func ModifyDrain(f func(*AttackContext, int64) (hp, mp int)) {
	modifiers.Lock()
	modifiers.drains = append(modifiers.drains, f)
	modifiers.Unlock()
}

func applyCeilingModifiers(dc DamageContext, c int) int {
	modifiers.RLock()
	fs := append([]func(DamageContext, int) int{}, modifiers.ceilings...)
	modifiers.RUnlock()
	for _, f := range fs {
		c = f(dc, c)
	}
	return max(0, c)
}

func drainers(spec *AttackSpec) []func(*AttackContext, int64) (int, int) {
	modifiers.RLock()
	out := append([]func(*AttackContext, int64) (int, int){}, modifiers.drains...)
	modifiers.RUnlock()
	if spec != nil && spec.Drain != nil {
		out = append(out, spec.Drain)
	}
	return out
}

// LevelSource is the skill whose learned level authorizes skillID's attack.
func LevelSource(skillID int, spec *AttackSpec) int {
	if spec != nil && spec.LevelFrom != 0 {
		return spec.LevelFrom
	}
	return skillID
}
