package content

import "maple-go-server/internal/domain"

// CanLearn reports whether a character of job may own this skill at all
// (family, branch and advancement tier). Level, SP and master level are
// checked by the caller.
func (s Skill) CanLearn(job int) bool { return domain.CanLearnSkill(job, s.ID) }

// CanLearnBy also opens the GM skill books (800/900/910) to GM-flagged
// characters, which QA grants set; their jobs stay ordinary so every other
// job-based rule (login roster, growth, QA gates) is unaffected.
func (s Skill) CanLearnBy(c domain.Character) bool {
	if s.CanLearn(c.Job) {
		return true
	}
	switch s.ID / 10000 {
	case 800, 900, 910:
		return c.GM > 0
	}
	return false
}
