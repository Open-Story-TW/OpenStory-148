package content

import (
	"fmt"
	"time"

	"maple-go-server/internal/domain"
)

// PhysicalAttackProfile covers reviewed warrior/Spearman ordinary attacks,
// Power Strike and Slash Blast. Loading another WZ skill does not enable it.
type PhysicalAttackProfile struct {
	SkillID, Level, MP, DamagePercent, MaxTargets, Range int
	strength, dexterity, attack, weaponFactor            int64
	potentialMaximum                                     PotentialPhysicalMaximum
}

func PhysicalAttackProfileFor(root string, c domain.Character, id int, now time.Time) (PhysicalAttackProfile, error) {
	p := PhysicalAttackProfile{SkillID: id, DamagePercent: 100, MaxTargets: 1, Range: 220}
	if (c.Job != 100 && c.Job != 130) || c.Level < 10 || c.Job == 130 && c.Level < 30 || c.HP <= 0 || id != 0 && id != 1001004 && id != 1001005 {
		return p, fmt.Errorf("unsupported physical attack job or skill")
	}
	p.strength, p.dexterity = int64(c.STR), int64(c.DEX)
	for _, e := range c.Equipment {
		// Cash appearance slots never replace the actual weapon or stats.
		if e.Slot >= 0 || e.Slot <= -100 {
			continue
		}
		p.strength += int64(e.STR)
		p.dexterity += int64(e.DEX)
		p.attack += int64(e.WATK)
		if e.Slot == -11 {
			switch e.ItemID / 10000 {
			case 130, 131, 132:
				p.weaponFactor = 120
			case 140, 141, 142:
				p.weaponFactor = 132
			case 143, 144:
				p.weaponFactor = 149
			default:
				return p, fmt.Errorf("unsupported first-warrior weapon")
			}
		}
	}
	var potentialErr error
	p.potentialMaximum, potentialErr = PreparePotentialPhysicalMaximum(root, c, now)
	if potentialErr != nil {
		return p, potentialErr
	}
	if p.weaponFactor == 0 || p.attack < 1 || p.strength < 1 || p.dexterity < 0 || p.potentialMaximum == nil && (p.attack > 100000 || p.strength > 100000 || p.dexterity > 100000) {
		return p, fmt.Errorf("invalid first-warrior attack stats")
	}
	if id == 0 {
		return p, nil
	}
	for _, s := range c.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			p.Level = s.Level
		}
	}
	definition, err := SkillDefinition(root, id)
	if err != nil || definition.Disabled || !definition.CanLearnBy(c) || p.Level < 1 || p.Level > definition.MaxLevel {
		return p, fmt.Errorf("physical skill is not learned or eligible")
	}
	v, err := definition.Values(p.Level)
	if err != nil || v["mpCon"] < 0 || v["mpCon"] > 1000 || v["damage"] < 1 || v["damage"] > 1000 {
		return p, fmt.Errorf("unsupported physical skill values")
	}
	p.MP, p.DamagePercent = v["mpCon"], v["damage"]
	if id == 1001005 {
		if v["mobCount"] < 1 || v["mobCount"] > 6 || v["range"] < 1 || v["range"] > 220 {
			return p, fmt.Errorf("unsupported slash-blast target bounds")
		}
		p.MaxTargets, p.Range = v["mobCount"], v["range"]
	}
	return p, nil
}

// MaximumDamage is a server-derived ceiling based on actual equipped stats,
// reference v145 weapon factors and WZ skill damage/monster PDRate. Critical,
// elemental, combo, passive and temporary physical effects are not enabled by
// these two skill families. Client damage is never accepted above this ceiling.
func (p PhysicalAttackProfile) MaximumDamage(m Mob) (int, error) {
	if m.PDRate < 0 || m.PDRate > 100 || m.Invincible || m.OnlyNormalAttack && p.SkillID != 0 || m.HasFixedDamage && m.FixedDamage >= 0 || m.FixedDamage > 0 {
		return 0, fmt.Errorf("unsupported monster physical damage policy")
	}
	if p.potentialMaximum != nil {
		return p.potentialMaximum(m, p.SkillID, p.DamagePercent)
	}
	numerator := (4*p.strength + p.dexterity) * p.attack * p.weaponFactor * int64(p.DamagePercent) * int64(100-m.PDRate)
	// Round upward once to avoid rejecting a legitimate fractional upper bound.
	return max(0, int((numerator+99999999)/100000000)), nil
}
