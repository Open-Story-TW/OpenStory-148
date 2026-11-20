package content

// AttackProcPotential describes a weapon/equipment potential line that procs
// one of the v148 monster-status skills (Skill.wz/9000.img: 90001001..90001006)
// when an attack hits a monster.
//
// ItemOption.img defines six attackType values (1000..1005):
//   - 1000 -> 90001003 (Poison)
//   - 1001 -> 90001001 (Stun)
//   - 1002 -> 90001002 (Slow / MobSpeed)
//   - 1003 -> 90001004 (Darkness)
//   - 1004 -> 90001006 (Freeze)
//   - 1005 -> 90001005 (Seal)
type AttackProcPotential struct {
	Source     PotentialEffectLine `json:"source"`
	AttackType int                 `json:"attack_type"`
	SkillID    int                 `json:"skill_id"`
	Level      int                 `json:"level"`
	Prop       int                 `json:"prop"`
}

// AttackProcSkillID maps an ItemOption attackType value (1000..1005) to its
// v148 Skill.wz/9000.img skill ID.
func AttackProcSkillID(attackType int) (int, bool) {
	switch attackType {
	case 1000:
		return 90001003, true // Poison
	case 1001:
		return 90001001, true // Stun
	case 1002:
		return 90001002, true // Slow
	case 1003:
		return 90001004, true // Darkness
	case 1004:
		return 90001006, true // Freeze
	case 1005:
		return 90001005, true // Seal
	default:
		return 0, false
	}
}

func attackProcPotential(line PotentialEffectLine) (AttackProcPotential, bool) {
	v := line.Attributes.Values
	attackType, hasType := v["attackType"]
	level, hasLevel := v["level"]
	prop, hasProp := v["prop"]
	if len(line.Attributes.Text) != 0 || len(v) != 3 || !hasType || !hasLevel || !hasProp {
		return AttackProcPotential{}, false
	}
	skillID, ok := AttackProcSkillID(attackType)
	if !ok || level < 1 || prop < 1 || prop > 100 {
		return AttackProcPotential{}, false
	}
	return AttackProcPotential{
		Source:     line,
		AttackType: attackType,
		SkillID:    skillID,
		Level:      level,
		Prop:       prop,
	}, true
}
