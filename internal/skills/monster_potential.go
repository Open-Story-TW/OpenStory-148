package skills

import (
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

const (
	MonsterPotentialDeath    = 90000000
	MonsterPotentialStun     = 90001001
	MonsterPotentialSlow     = 90001002
	MonsterPotentialPoison   = 90001003
	MonsterPotentialDarkness = 90001004
	MonsterPotentialSeal     = 90001005
	MonsterPotentialFreeze   = 90001006
)

// MonsterPotentialStatusSpec builds the combat.MobStatusApply for one of the
// six v148 monster-status potential skills (Skill.wz/9000.img 90001001..90001006).
// When propOverride > 0 (from an ItemOption attackType line), it replaces the
// skill's own WZ prop.
func MonsterPotentialStatusSpec(skillID, level, propOverride int) (combat.MobStatusApply, bool) {
	if level < 1 {
		level = 1
	}
	vals := map[string]int{}
	if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID); err == nil {
		if v, err := def.Values(level); err == nil {
			vals = v
		} else if v, err := def.Values(1); err == nil {
			vals = v
		}
	}
	valOr := func(key string, fallback int) int {
		if v, ok := vals[key]; ok && v != 0 {
			return v
		}
		return fallback
	}
	prop := propOverride
	if prop <= 0 {
		prop = valOr("prop", 10)
	}
	switch skillID {
	case MonsterPotentialStun:
		dur := 10
		if level >= 2 {
			dur = 6
		}
		return combat.MobStatusApply{
			Stat:        combat.MobStun,
			Value:       valOr("x", 1),
			Duration:    time.Duration(valOr("time", dur)) * time.Second,
			Prop:        prop,
			SourceSkill: skillID,
		}, true
	case MonsterPotentialSlow:
		return combat.MobStatusApply{
			Stat:        combat.MobSpeed,
			Value:       valOr("x", -50),
			Duration:    time.Duration(valOr("time", 10)) * time.Second,
			Prop:        prop,
			SourceSkill: skillID,
		}, true
	case MonsterPotentialPoison:
		return combat.MobStatusApply{
			Stat:        combat.MobPoison,
			Value:       1,
			Duration:    time.Duration(valOr("dotTime", 10)) * time.Second,
			Prop:        prop,
			Poison:      true,
			DOTPercent:  valOr("dot", 40),
			DOTInterval: time.Duration(valOr("dotInterval", 1)) * time.Second,
			SourceSkill: skillID,
		}, true
	case MonsterPotentialDarkness:
		x, dur := 70, 10
		if level >= 2 {
			x, dur = 80, 8
		}
		return combat.MobStatusApply{
			Stat:        combat.MobDarkness,
			Value:       valOr("x", x),
			Duration:    time.Duration(valOr("time", dur)) * time.Second,
			Prop:        prop,
			SourceSkill: skillID,
		}, true
	case MonsterPotentialSeal:
		return combat.MobStatusApply{
			Stat:        combat.MobSeal,
			Value:       1,
			Duration:    time.Duration(valOr("time", 10)) * time.Second,
			Prop:        prop,
			SourceSkill: skillID,
		}, true
	case MonsterPotentialFreeze:
		dur := 10
		if level >= 2 {
			dur = 5
		}
		return combat.MobStatusApply{
			Stat:        combat.MobFreeze,
			Value:       1,
			Duration:    time.Duration(valOr("time", dur)) * time.Second,
			Prop:        prop,
			SourceSkill: skillID,
		}, true
	default:
		return combat.MobStatusApply{}, false
	}
}

func attachMonsterPotentialProcs(p *combat.PlayerStats, c domain.Character, procs []content.AttackProcPotential) {
	appendProc := func(a combat.MobStatusApply) {
		if p.OnHitProcCount < len(p.OnHitProcs) {
			p.OnHitProcs[p.OnHitProcCount] = a
			p.OnHitProcCount++
		}
	}
	for _, proc := range procs {
		if a, ok := MonsterPotentialStatusSpec(proc.SkillID, proc.Level, proc.Prop); ok {
			appendProc(a)
		}
	}
	for _, s := range c.Skills {
		if s.Level <= 0 {
			continue
		}
		if s.ID == MonsterPotentialDeath {
			p.PotentialInstantKill = true
			continue
		}
		if a, ok := MonsterPotentialStatusSpec(s.ID, s.Level, 0); ok {
			appendProc(a)
		}
	}
}
