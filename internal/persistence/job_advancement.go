package persistence

import (
	"context"
	"maple-go-server/internal/domain"
)

var _ domain.JobAdvancementRepository = (*Store)(nil)
var _ domain.FirstJobAdvancementRepository = (*Store)(nil)

// AdvanceWarrior preserves the original typed host contract while sharing the
// same source-bound receipt, rewards, and authority checks as other first jobs.
func (s *Store) AdvanceWarrior(ctx context.Context, r domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	if r.NPCID != 1022000 || r.Script != "npc/1022000.lua" || r.TargetJob != 0 && r.TargetJob != 100 {
		return domain.JobAdvancementResult{}, domain.ErrForbidden
	}
	r.TargetJob = 100
	return s.AdvanceFirstJob(ctx, r)
}

// changeJob resets the script's earlier STR35 reset back to four in each
// stat. All spent points are conserved in AP; no retroactive SP is granted.
func firstWarriorStats(c domain.Character, operationID string) (domain.Character, error) {
	return firstJobStats(c, operationID, firstJobPolicy{job: 100, minLevel: 10})
}
