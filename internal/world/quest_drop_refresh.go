package world

import (
	"context"
	"log/slog"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

type questRefreshRequest struct {
	key    mapKey
	token  uint64
	before domain.Character
}
type questRefreshWorker struct {
	pending *questRefreshRequest
	cancel  context.CancelFunc
}

func activeDropQuests(c domain.Character) map[int]bool {
	m := map[int]bool{}
	for _, q := range c.Quests {
		if q.State == 1 {
			m[q.ID] = true
		}
	}
	return m
}
func itemDropTotals(c domain.Character) map[int]int {
	m := map[int]int{}
	for _, i := range c.Items {
		m[i.TemplateID] += i.Quantity
	}
	return m
}
func differentDropQuests(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return true
	}
	for id := range a {
		if !b[id] {
			return true
		}
	}
	return false
}

// Pure bounded comparison: bag rearrangement, HP and mob counters do not queue
// work. WZ evaluation and all SQL happen in the worker, outside caller locks.
func possibleQuestDropChange(a, b domain.Character) bool {
	x, y := activeDropQuests(a), activeDropQuests(b)
	if differentDropQuests(x, y) {
		return true
	}
	if len(x) == 0 {
		return false
	}
	left, right := itemDropTotals(a), itemDropTotals(b)
	if len(left) != len(right) {
		return true
	}
	for id, n := range left {
		if right[id] != n {
			return true
		}
	}
	return false
}

func relevantQuestDropChange(a, b domain.Character) bool {
	x, y := activeDropQuests(a), activeDropQuests(b)
	if differentDropQuests(x, y) {
		return true
	}
	left, right := itemDropTotals(a), itemDropTotals(b)
	for id := range y {
		q, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
		if err != nil {
			continue
		}
		for _, i := range q.Complete.Items {
			if i.Count > 0 && left[i.ID] != right[i.ID] {
				return true
			}
		}
	}
	return false
}

func (s *Server) updateQuestDropSession(session domain.CharacterSession) {
	before, updated := s.drops.updateSession(session)
	if !updated || !possibleQuestDropChange(before, session.Character) {
		return
	}
	d := s.drops
	key := mapKey{session.ChannelID, session.Character.MapID}
	d.mu.Lock()
	track := d.questTracks[key][session.CharacterID]
	if track == nil || track.session.SessionID != session.SessionID || track.session.Generation != session.Generation {
		d.mu.Unlock()
		return
	}
	request := &questRefreshRequest{key: key, token: track.token, before: before}
	if d.questRefresh == nil {
		d.questRefresh = map[int64]*questRefreshWorker{}
	}
	worker := d.questRefresh[session.CharacterID]
	start := worker == nil
	if start {
		worker = &questRefreshWorker{}
		d.questRefresh[session.CharacterID] = worker
	}
	if worker.pending != nil && worker.pending.token == request.token {
		request.before = worker.pending.before
	}
	worker.pending = request
	if worker.cancel != nil {
		worker.cancel()
	}
	d.mu.Unlock()
	if start {
		go s.runQuestDropRefresh(session.CharacterID, worker)
	}
}

// One worker per character, including across replacement map entries. New
// registrations cancel stale work and replace its pending request; they cannot
// start a parallel worker which races an old snapshot onto the new socket.
func (s *Server) runQuestDropRefresh(id int64, worker *questRefreshWorker) {
	d := s.drops
	for {
		d.mu.Lock()
		request := worker.pending
		worker.pending = nil
		if request == nil {
			delete(d.questRefresh, id)
			d.mu.Unlock()
			return
		}
		track := d.questTracks[request.key][id]
		if track == nil || track.token != request.token {
			d.mu.Unlock()
			continue
		}
		session, sink := track.session, track.sink
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		worker.cancel = cancel
		d.mu.Unlock()
		if relevantQuestDropChange(request.before, session.Character) && ctx.Err() == nil {
			if err := s.refreshQuestDrops(ctx, session, sink, request.token); err != nil && ctx.Err() == nil {
				slog.Warn("quest_drop_refresh_failed", "character", id, "error", err)
			}
		}
		cancel()
		d.mu.Lock()
		worker.cancel = nil
		d.mu.Unlock()
	}
}
