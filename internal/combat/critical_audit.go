package combat

import (
	"math"
	"sync"
)

// The client rolls criticals itself and sends no flag, so one line can never be
// proven honest: a value above the noncritical bound might be a critical or a
// forged number. Over many lines it can: only criticals get there, so such lines
// cannot be more frequent than the attacker's critical rate allows.

const (
	criticalAuditWindow = 256
	criticalAuditBase   = 5 // The client's cr starts at 5 whatever the passives.
	// Slack for a run of honest luck: four standard deviations plus a few lines,
	// so a real attacker is practically never clipped.
	criticalAuditDeviations = 4
	criticalAuditSpare      = 3
)

// CriticalAudit keeps each character's recent lines. The zero value is ready.
type CriticalAudit struct {
	mu    sync.Mutex
	chars map[int64]*criticalWindow
}

type criticalWindow struct {
	lines [criticalAuditWindow]bool // true: above the noncritical bound.
	next  int
	seen  int
	over  int
}

func (w *criticalWindow) push(over bool) {
	if w.seen == criticalAuditWindow {
		if w.lines[w.next] {
			w.over--
		}
	} else {
		w.seen++
	}
	w.lines[w.next] = over
	if over {
		w.over++
	}
	w.next = (w.next + 1) % criticalAuditWindow
}

func (a *CriticalAudit) window(character int64) *criticalWindow {
	if a.chars == nil {
		a.chars = make(map[int64]*criticalWindow)
	}
	w := a.chars[character]
	if w == nil {
		w = &criticalWindow{}
		a.chars[character] = w
	}
	return w
}

// Normal records a line a noncritical hit can reach.
func (a *CriticalAudit) Normal(character int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.window(character).push(false)
}

// Admit reports whether one more line above the noncritical bound fits the
// character's critical rate. A refused line counts as normal: it is clipped to
// the noncritical bound, so a forger keeps only what the rate would have given.
func (a *CriticalAudit) Admit(character int64, critRate int) bool {
	p := float64(criticalAuditBase+max(0, critRate)) / 100
	if p >= 1 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.window(character)
	n := float64(w.seen + 1)
	limit := n*p + criticalAuditDeviations*math.Sqrt(n*p*(1-p)) + criticalAuditSpare
	ok := float64(w.over+1) <= limit
	w.push(ok)
	return ok
}
