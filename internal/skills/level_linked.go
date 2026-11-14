package skills

import (
	"time"

	"maple-go-server/internal/domain"
)

// LinkedLevel is EffectiveLevel for a skill whose level is learned through
// learnedID (AttackSpec/Handler.LevelFrom): the learned level capped at id's
// MaxLevel, or with COMBAT_ORDERS added up to id's TrueMax. Attack planning
// (world) and settlement bounds (persistence) both use it.
func LinkedLevel(c domain.Character, learnedID, id int, now time.Time) int {
	return levelFor(c, learnedID, id, now)
}
