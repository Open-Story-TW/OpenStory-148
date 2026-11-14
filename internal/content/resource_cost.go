package content

import (
	"fmt"
	"math"
	"time"
)

// ResourceCostModifiers is a detached snapshot prepared before taking a room
// lock. MPConsumePercent includes the original base100; MPReductionPercent is
// applied after the native integer MPConsume multiplier. Demon Force is never ordinary MP.
type ResourceCostModifiers struct {
	Prepared                            bool
	MPConsumePercent                    int
	MPReductionPercent                  int
	CooldownReductionSeconds            int
	DemonForce, Infinity, BoundlessRage bool
	TeleportExtraMP                     int
	MechanicMode                        bool
	MechanicMP                          [3]int // 35111004,35121005,35121013; last uses learned35121005.w.
}

type ResourceCost struct {
	MP       int
	Cooldown time.Duration
}

// CalculateResourceCost follows native68FE61@69038F/6904FD/69050D: integer
// multiplier first, then all reducers, then clamp0. This intentionally differs
// from Java3064 (reverse order and a negative-cost healing bug over100%).
// Cooldown reduction follows MapleStatEffect.getCooldown3389/native807106.
func CalculateResourceCost(skillID, mp int, cooldown time.Duration, m ResourceCostModifiers) (ResourceCost, error) {
	if !m.Prepared {
		m.MPConsumePercent = 100
	}
	if mp < 0 || int64(mp) > math.MaxInt32 || cooldown < 0 || cooldown > 24*time.Hour ||
		m.MPConsumePercent < 0 || m.MPConsumePercent > 10000 || m.MPReductionPercent < 0 || int64(m.MPReductionPercent) > math.MaxInt32 ||
		m.CooldownReductionSeconds < 0 || m.CooldownReductionSeconds > 86400 || m.TeleportExtraMP < 0 || int64(m.TeleportExtraMP) > math.MaxInt32 {
		return ResourceCost{}, fmt.Errorf("unsupported skill resource cost")
	}
	out := ResourceCost{Cooldown: max(time.Duration(0), cooldown-time.Duration(m.CooldownReductionSeconds)*time.Second)}
	if m.DemonForce {
		if !m.BoundlessRage {
			out.MP = mp
		}
		return out, nil
	}
	percent := 100
	if NativeMPConsumeApplies(skillID) {
		percent = m.MPConsumePercent
	}
	n := int64(mp) * int64(percent) / 100
	if m.Infinity {
		n = 0
	}
	if m.MechanicMode {
		index := -1
		switch skillID {
		case 35111004:
			index = 0
		case 35121005:
			index = 1
		case 35121013:
			index = 2
		}
		if index >= 0 {
			v := m.MechanicMP[index]
			if v < 0 || int64(v) > math.MaxInt32 {
				return ResourceCost{}, fmt.Errorf("invalid mechanic attack MP cost")
			}
			n = int64(v)
		}
	}
	if NativeTeleportExtraMPApplies(skillID) && !m.Infinity {
		n += int64(m.TeleportExtraMP)
	}
	if n > math.MaxInt32 {
		return ResourceCost{}, fmt.Errorf("skill resource cost overflow")
	}
	n = max(int64(0), n-n*int64(m.MPReductionPercent)/100)
	out.MP = int(n)
	return out, nil
}

// NativeTeleportExtraMPApplies is669C3C, used at69046C before the reducers.
func NativeTeleportExtraMPApplies(id int) bool {
	switch id {
	case 2101002, 2201002, 2301001, 9001007, 8001001, 12101003, 22101001, 32001002:
		return true
	}
	return false
}

// NativeMPConsumeApplies is the exact40-skill predicate in68EB89 that writes
// the MP multiplier output (WZ X); all other skills retain100. The function's
// damage multiplier return (WZ Y) is a separate consumer and is not reused.
func NativeMPConsumeApplies(id int) bool {
	switch id {
	case 1076, 2001004, 2001005, 2101004, 2101005, 2111002, 2111003, 2111006, 2121001, 2121003, 2121006, 2121007,
		2201004, 2201005, 2211002, 2211003, 2211006, 2221001, 2221003, 2221006, 2221007,
		12001003, 12101002, 12101006, 12111003, 12111005, 12111006, 22001001, 22101000, 22111000,
		22121000, 22131000, 22141001, 22151001, 22151002, 22161001, 22171002, 22171003, 22181001, 22181002:
		return true
	}
	return false
}
