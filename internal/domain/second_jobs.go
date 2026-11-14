package domain

import "context"

// SecondJobAdvancementRepository implements only audited source-bound choices,
// not a general job setter. The initial supported branch is 100 -> 130 through
// NPC1052001's existing custom Job Advancer in map103000003.
type SecondJobAdvancementRepository interface {
	AdvanceSecondJob(context.Context, JobAdvancementRequest) (JobAdvancementResult, error)
}

// SecondJobOutcomeRepository resolves an uncertain commit under the same
// authority locks. It never executes the advancement policy a second time.
type SecondJobOutcomeRepository interface {
	ResolveSecondJob(context.Context, JobAdvancementRequest) (JobAdvancementResult, bool, error)
}
