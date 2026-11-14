package content

import (
	"fmt"
	"maple-go-server/internal/domain"
	"time"
)

// NormalAttackMaximum retains the implemented beginner ceiling and reviewed
// warrior profile. Other job attack families require their own authority rules.
func NormalAttackMaximum(root string, c domain.Character, m Mob, now time.Time) (int, error) {
	maximum, err := PrepareNormalAttackMaximum(root, c, now)
	if err != nil {
		return 0, err
	}
	return maximum(m)
}

// PrepareNormalAttackMaximum freezes the same checked stats for every target,
// outside the world room lock. Store uses it through NormalAttackMaximum.
//
// A beginner's ceiling uses the full combat view (equipment, passives and
// buffs) through PotentialPhysicalBoundsProvider, the same bound the generic
// attack path uses. Counting only base STR and the weapon's attack clipped
// every hit of a beginner with stat equipment to a few points while the
// client showed hundreds, so low-level monsters seemed to have thousands of HP.
func PrepareNormalAttackMaximum(root string, c domain.Character, now time.Time) (func(Mob) (int, error), error) {
	if c.Job == 100 || c.Job == 130 {
		p, err := PhysicalAttackProfileFor(root, c, 0, now)
		if err != nil {
			return nil, err
		}
		return p.MaximumDamage, nil
	}
	if c.Job != 0 || c.HP <= 0 {
		return nil, fmt.Errorf("unsupported ordinary attack")
	}
	potential, err := PreparePotentialPhysicalMaximum(root, c, now)
	if err != nil {
		return nil, err
	}
	if potential == nil && PotentialPhysicalBoundsProvider != nil {
		if potential, err = PotentialPhysicalBoundsProvider(c, now); err != nil {
			return nil, err
		}
		if potential == nil {
			return nil, fmt.Errorf("checked beginner provider returned no bound")
		}
	}
	strength, dexterity, attack, armed := int64(c.STR), int64(c.DEX), int64(0), false
	for _, e := range c.Equipment {
		// Cash appearance slots never add stats.
		if e.Slot >= 0 || e.Slot <= -100 {
			continue
		}
		strength += int64(e.STR)
		dexterity += int64(e.DEX)
		attack += int64(e.WATK)
		armed = armed || e.Slot == -11
	}
	if !armed {
		attack = 1 // The client multiplies bare hands by 1, not PAD.
	}
	return func(m Mob) (int, error) {
		if m.Invincible || m.HasFixedDamage || m.FixedDamage > 0 {
			return 0, fmt.Errorf("unsupported ordinary monster")
		}
		if potential != nil {
			return potential(m, 0, 100)
		}
		// Content-only fallback (no skills package linked): the client's
		// beginner formula, any weapon scored ×1.20, less the monster's PDRate.
		rate := int64(max(0, min(100, m.PDRate)))
		numerator := (4*strength + dexterity) * max(1, attack) * 120 * (100 - rate)
		return max(1, int((numerator+999999)/1000000)), nil
	}, nil
}
