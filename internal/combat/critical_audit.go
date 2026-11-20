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

// criticalWindow keeps each recent line with the critical chance it was hit
// at, so lines hit under different rates (a riding or Sharp Eyes stretch, then
// a skill without them) are judged against their own expected count.
type criticalWindow struct {
	lines    [criticalAuditWindow]auditLine
	next     int
	seen     int
	over     int
	expected float64 // Sum of p over the window.
	variance float64 // Sum of p(1-p) over the window.
}

type auditLine struct {
	over bool // Above the noncritical bound.
	p    float64
}

func (w *criticalWindow) push(over bool, p float64) {
	if w.seen == criticalAuditWindow {
		old := w.lines[w.next]
		if old.over {
			w.over--
		}
		w.expected -= old.p
		w.variance -= old.p * (1 - old.p)
	} else {
		w.seen++
	}
	w.lines[w.next] = auditLine{over: over, p: p}
	if over {
		w.over++
	}
	w.expected += p
	w.variance += p * (1 - p)
	w.next = (w.next + 1) % criticalAuditWindow
}

func auditChance(critRate int) float64 {
	return min(1, float64(criticalAuditBase+max(0, critRate))/100)
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

// Normal records a line a noncritical hit can reach, hit at critRate.
func (a *CriticalAudit) Normal(character int64, critRate int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.window(character).push(false, auditChance(critRate))
}

// Admit reports whether one more line above the noncritical bound fits the
// character's critical rate. A refused line counts as normal: it is clipped to
// the noncritical bound, so a forger keeps only what the rate would have given.
func (a *CriticalAudit) Admit(character int64, critRate int) bool {
	p := auditChance(critRate)
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.window(character)
	if p >= 1 {
		w.push(true, 1)
		return true
	}
	limit := w.expected + p + criticalAuditDeviations*math.Sqrt(w.variance+p*(1-p)) + criticalAuditSpare
	ok := float64(w.over+1) <= limit
	w.push(ok, p)
	return ok
}
