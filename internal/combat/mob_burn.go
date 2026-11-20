package combat

import (
	"cmp"
	"slices"
	"time"

	"maple-go-server/internal/protocol"
)

type mobBurnSource struct {
	owner int64
	skill int32
}

// MobBurns holds one entry per native source owner and skill. The stored
// TickDamage is per stack, so the wire and server tick use the same inputs.
type MobBurns map[mobBurnSource]*ActiveStatus

func (m MobBurns) Apply(s ActiveStatus, now time.Time) bool {
	if s.Stat != MobBurned || s.OwnerID <= 0 || s.OwnerID > 0x7fffffff ||
		s.SkillID <= 0 || s.SkillID > 0x0fffffff ||
		s.TickDamage <= 0 || s.Interval < time.Millisecond || s.Interval > 10*time.Minute ||
		!s.ExpiresAt.After(now) {
		return false
	}
	source := mobBurnSource{owner: s.OwnerID, skill: s.SkillID}
	old := m[source]
	if old == nil && len(m) >= 255 {
		return false
	}
	s.TickDamage = min(s.TickDamage, 0x7fffffff)
	s.Value = int32(s.TickDamage)
	s.Stacks = 1
	if old != nil && old.ExpiresAt.After(now) && old.OwnerSession == s.OwnerSession && old.OwnerGeneration == s.OwnerGeneration {
		s.Stacks = min(old.Stacks+1, max(1, min(15, s.StackLimit)))
		s.NextTick = old.NextTick
	}
	m[source] = &s
	return true
}

func (m MobBurns) Tick(now time.Time, hp int64) (int64, []DoTHit) {
	var hits []DoTHit
	for _, s := range m {
		for !s.NextTick.After(now) && !s.NextTick.After(s.ExpiresAt) {
			if hp > 1 {
				d := min(s.TickDamage*int64(s.Stacks), hp-1)
				hp -= d
				hits = append(hits, s.doTHit(d))
			}
			s.NextTick = s.NextTick.Add(s.Interval)
		}
	}
	return hp, hits
}

func (m MobBurns) Values() []protocol.MobBurn {
	values := make([]protocol.MobBurn, 0, len(m))
	for _, s := range m {
		values = append(values, s.burnValue())
	}
	slices.SortFunc(values, func(a, b protocol.MobBurn) int {
		if c := cmp.Compare(a.OwnerID, b.OwnerID); c != 0 {
			return c
		}
		return cmp.Compare(a.SkillID, b.SkillID)
	})
	return values
}

func (m MobBurns) Expire(now time.Time) []protocol.MobBurn {
	var ended []protocol.MobBurn
	for k, s := range m {
		if !s.ExpiresAt.After(now) {
			ended = append(ended, s.burnValue())
			delete(m, k)
		}
	}
	return ended
}

func (s *ActiveStatus) burnValue() protocol.MobBurn {
	ticks := 0
	if !s.NextTick.After(s.ExpiresAt) {
		ticks = 1 + int(s.ExpiresAt.Sub(s.NextTick)/s.Interval)
	}
	return protocol.MobBurn{OwnerID: int32(s.OwnerID), SkillID: s.SkillID, Damage: s.Value,
		Interval: s.Interval, ExpiresAt: s.ExpiresAt, RemainingTicks: ticks, Stacks: s.Stacks}
}
