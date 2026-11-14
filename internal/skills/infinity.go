package skills

import (
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func IsInfinity(id int) bool { return id == 2121004 || id == 2221004 || id == 2321004 }

func infinityMagicPercent(c domain.Character, now time.Time) int {
	for _, b := range c.SkillBuffs {
		if b.Stat == "INFINITY" && b.ExpiresAt.After(now) {
			return max(0, b.Value-1)
		}
	}
	return 0
}

// AdvanceInfinity implements one Java CancelEffectAction timer callback, not
// elapsed-time catch-up. A resumed legacy row with no timer is armed without
// granting offline recovery. Expiry and explicit cancellation never tick.
// The caller commits value, next_tick and capped vitals in the same transaction.
func AdvanceInfinity(root string, b domain.SkillBuff, now time.Time, maxHP, maxMP int) (domain.SkillBuff, int, int, error) {
	if !IsInfinity(b.ID) || b.Stat != "INFINITY" || !b.ExpiresAt.After(now) || b.NextTick.After(now) {
		return b, 0, 0, nil
	}
	if b.NextTick.IsZero() {
		b.NextTick = now.Add(4 * time.Second)
		return b, 0, 0, nil
	}
	def, err := content.SkillDefinition(root, b.ID)
	if err != nil {
		return b, 0, 0, err
	}
	values, err := def.Values(b.Level)
	if err != nil {
		return b, 0, 0, err
	}
	step, recovery := values["damage"], values["y"]
	if step < 0 || recovery < 0 || recovery > 100 || b.Value < 1 || b.Value > 32767-step || maxHP < 0 || maxMP < 0 {
		return b, 0, 0, fmt.Errorf("invalid Infinity timer state")
	}
	b.Value += step
	// Keep the original cadence across sub-interval scheduler jitter. A
	// delayed callback never replays missed heals: after a whole extra missed
	// interval, start a new deadline from now instead of catching up.
	next := b.NextTick.Add(4 * time.Second)
	if !next.After(now) {
		next = now.Add(4 * time.Second)
	}
	b.NextTick = next
	return b, int(int64(maxHP) * int64(recovery) / 100), int(int64(maxMP) * int64(recovery) / 100), nil
}
