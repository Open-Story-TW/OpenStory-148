package content

import (
	"fmt"
	"maple-go-server/internal/domain"
	"math"
)

// PotionRecoveryModifiers is a detached additive bucket. It applies only to
// positive primary item fixed HP, never to hpR, MP, skill Heal or periodic HP.
// Java MapleStatEffect3008/3094 and PlayerStats3545 are the reviewed policy;
// native RecoveryUP application arithmetic remains unverified.
type PotionRecoveryModifiers struct{ FixedHPPercent int }

type PotionVitals struct{ HP, MP, MaxHP, MaxMP int }

// CalculatePotionRecovery clamps a trusted live value to newly-derived maxima
// before recovery. The flat component is scaled once, then hpR is added, then
// the final HP is capped. Demon Fury never receives item MP.
func CalculatePotionRecovery(r Recovery, v PotionVitals, m PotionRecoveryModifiers, demon bool) (PotionVitals, error) {
	if v.HP <= 0 || v.MP < 0 || v.MaxHP < 1 || v.MaxHP > domain.MaxVital() || v.MaxMP < 0 || v.MaxMP > domain.MaxVital() ||
		r.HP < 0 || r.HP > math.MaxInt32 || r.MP < 0 || r.MP > math.MaxInt32 || r.HPRate < 0 || r.HPRate > 100 || r.MPRate < 0 || r.MPRate > 100 ||
		m.FixedHPPercent < 0 || m.FixedHPPercent > math.MaxInt32-100 {
		return PotionVitals{}, fmt.Errorf("invalid positive item recovery")
	}
	// Preserve Java integer truncation, using checked wider intermediates rather
	// than reproducing its signed32 overflow for corrupt/out-of-scope inputs.
	flat := int64(r.HP) * (100 + int64(m.FixedHPPercent)) / 100
	if flat > math.MaxInt32 {
		return PotionVitals{}, fmt.Errorf("fixed item HP recovery overflow")
	}
	v.HP, v.MP = min(v.HP, v.MaxHP), min(v.MP, v.MaxMP)
	v.HP += int(min(int64(v.MaxHP-v.HP), flat+int64(v.MaxHP)*int64(r.HPRate)/100))
	if !demon {
		v.MP += int(min(int64(v.MaxMP-v.MP), int64(r.MP)+int64(v.MaxMP)*int64(r.MPRate)/100))
	}
	return v, nil
}
