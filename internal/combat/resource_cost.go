package combat

import (
	"fmt"
	"time"

	"maple-go-server/internal/content"
)

// PreparedResourceCost consumes an immutable stat snapshot after the skill's
// original Prepare. It performs no WZ/global/provider read under the room lock.
func PreparedResourceCost(spec *AttackSpec, ctx AttackContext) (content.ResourceCost, error) {
	if ctx.Kind == Summoned {
		if spec == nil || spec.Summon == nil || spec.Kind&Summoned == 0 {
			return content.ResourceCost{}, ErrKind
		}
		return content.ResourceCost{}, nil // Registered summon; its cast already paid.
	}
	m := ctx.Stats.ResourceCosts
	if !m.Prepared {
		// Compatibility callers without a checked provider retain the old
		// equipment-only path, but cannot zero an active potential cost source.
		for _, e := range ctx.Character.Equipment {
			if e.Slot < 0 && e.Slot > -100 && e.CashID == 0 && (e.ExpiresAt == 0 || e.ExpiresAt > ctx.Now.UnixMilli()) && e.Potential.RawState&0x10 != 0 {
				return content.ResourceCost{}, fmt.Errorf("resource cost snapshot unavailable")
			}
		}
		_, m.DemonForce = content.DemonFuryMax(ctx.Character.Job)
		for _, row := range ctx.Character.SkillBuffs {
			if !row.ExpiresAt.After(ctx.Now) {
				continue
			}
			switch row.Stat {
			case "INFINITY":
				m.Infinity = row.Value != 0
			case "BOUNDLESS_RAGE":
				m.BoundlessRage = row.Value > 0
			case "CONCENTRATE", "HOLY_SHIELD":
				return content.ResourceCost{}, fmt.Errorf("resource buff snapshot unavailable")
			}
		}
	}
	mp := ctx.Effect.MPCon
	// Only reviewed cost-only rules are mirrored by persistence. Do not run
	// general AttackSpec.Prepare there: it may roll projectile conservation or
	// mutate damage. The full world Prepare still runs exactly once.
	switch ctx.Effect.SkillID {
	case 2301002: // Heal's cast pays; its subsequent undead attack does not.
		mp = 0
	case 32111010, 32121003, 31121005, 35121003:
		// SG-88's SUMMON alias is stored under the wire canonical name COMBO.
		stat := map[int]string{32111010: "TELEPORT_MASTERY", 32121003: "TORNADO", 31121005: "DARK_METAMORPHOSIS", 35121003: "COMBO"}[ctx.Effect.SkillID]
		active := false
		for _, row := range ctx.Character.SkillBuffs {
			matches := row.Stat == stat || stat == "COMBO" && row.Stat == "SUMMON"
			active = active || row.ID == ctx.Effect.SkillID && matches && row.ExpiresAt.After(ctx.Now)
		}
		if !active {
			return content.ResourceCost{}, fmt.Errorf("paid attack needs active source cast")
		}
		mp = 0
	}
	if m.DemonForce && !ctx.Effect.ForceCost {
		mp = 0
	}
	cd := ctx.Effect.Cooltime
	if spec != nil && spec.NoCooldown {
		cd = 0
	}
	return content.CalculateResourceCost(ctx.Effect.SkillID, mp, cd, m)
}

// PrepareResourceCost reconstructs the checked cost-only rules, never general
// skill preparation, on-hit rolls or damage. It cannot reroll projectile RNG.
func PrepareResourceCost(spec *AttackSpec, ctx AttackContext) (content.ResourceCost, error) {
	if !spec.Allows(ctx.Kind) {
		return content.ResourceCost{}, ErrKind
	}
	return PreparedResourceCost(spec, ctx)
}

// tempestOfCard is Phantom's keydown spin (24121005): WZ cooltime is 12..15s
// while the held spin fires melee ticks every ~180ms for up to 5s without a
// separate self buff. Cooldown starts on the first tick; ticks inside the 5s
// spin window belong to that cast and do not restart or fail on cooldown.
const (
	tempestOfCard        = 24121005
	tempestKeydownWindow = 5 * time.Second
)

// OngoingResourceCast verifies that cooldown belongs to an already-paid cast.
func OngoingResourceCast(ctx AttackContext) bool {
	if ctx.Effect.SkillID == 0 {
		return false
	}
	if ctx.Kind == Body {
		return true
	}
	for _, row := range ctx.Character.SkillBuffs {
		if row.ID == ctx.Effect.SkillID && row.ExpiresAt.After(ctx.Now) {
			return true
		}
	}
	if ctx.Effect.SkillID == tempestOfCard {
		effectiveCD := max(time.Duration(0), ctx.Effect.Cooltime-time.Duration(ctx.Stats.ResourceCosts.CooldownReductionSeconds)*time.Second)
		if effectiveCD > tempestKeydownWindow {
			for _, cd := range ctx.Character.SkillCooldowns {
				if cd.ID == tempestOfCard && cd.ExpiresAt.After(ctx.Now) && cd.ExpiresAt.Sub(ctx.Now) > effectiveCD-tempestKeydownWindow {
					return true
				}
			}
		}
	}
	return false
}

func ResourceCooldown(c content.ResourceCost, ongoing bool) time.Duration {
	if ongoing {
		return 0
	}
	return c.Cooldown
}
