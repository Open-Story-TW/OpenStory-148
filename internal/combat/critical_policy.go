package combat

import "maple-go-server/internal/domain"

// ReviewedWireCriticality is intentionally restricted to the 1001004 and
// 1311001 melee senders proved by 2026-11-06 native wire/local-hit pairs. Other families
// retain their existing policy until independently reviewed.
func ReviewedWireCriticality(kind AttackKind, skill int, raw uint32) domain.CombatCriticality {
	if kind != Melee || skill != 1001004 && skill != 1311001 {
		return ""
	}
	if raw&0x80000000 != 0 {
		return domain.CombatCriticalExplicit
	}
	return domain.CombatCriticalUnknown
}

// WireLineBounds does not roll criticals or change raw damage. The v145 client
// computes criticals locally and sends only the damage: a critical and a normal
// frame differ in the value alone (native wire/local-hit pairs, 2026-11-07), so a
// clear bit is never evidence of a noncritical hit. Bound it by the legal critical
// envelope, while leaving fixed/immune policies and summons alone.
func WireLineBounds(c DamageContext, kind AttackKind, raw uint32, floor, ceiling int) (int, int) {
	if raw&0x80000000 != 0 {
		return CritFloor(floor), CritCeiling(c, ceiling)
	}
	if UnmarkedMayBeCritical(c, kind) {
		// Keep the normal floor: the unclassified line may be noncritical.
		return floor, CritCeiling(c, ceiling)
	}
	return floor, ceiling
}

// UnmarkedMayBeCritical reports whether a line without the wire bit can still be
// a client critical: every attack the client rolls itself.
func UnmarkedMayBeCritical(c DamageContext, kind AttackKind) bool {
	return kind != Summoned && c.Effect.FixDamage == 0 && !c.Mob.Invincible && !c.Mob.HasFixedDamage && c.Mob.FixedDamage == 0 &&
		!c.Mob.OnlyNormalAttack && !c.Statuses.BlocksDamage(c.Magic)
}
