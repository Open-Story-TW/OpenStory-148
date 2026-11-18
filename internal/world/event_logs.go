package world

import (
	"cmp"
	"context"
	"slices"

	"maple-go-server/internal/domain"
)

func (s *Server) eventRosterBelowLogLimit(ctx context.Context, group []eventOccupant, key domain.ScriptLogKey, maximum int64) bool {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok || len(group) == 0 {
		return false
	}
	for _, member := range group {
		logs, err := repo.ReadScriptLogs(ctx, s.skillSession(member.live), []domain.ScriptLogKey{key})
		if err != nil || len(logs) != 1 || logs[0].Value >= maximum {
			return false
		}
	}
	return true
}

func (s *Server) exchangeEventRosterLog(ctx context.Context, inst *eventInstance, group []eventOccupant, mapID int, action string, change domain.ScriptLogChange) error {
	repo, ok := s.repo.(domain.EventLogRepository)
	if !ok || len(group) == 0 {
		return domain.ErrForbidden
	}
	slices.SortFunc(group, func(a, b eventOccupant) int {
		return cmp.Compare(partySession(a.live).CharacterID, partySession(b.live).CharacterID)
	})
	for _, member := range group {
		member.live.persistMu.Lock()
		defer member.live.persistMu.Unlock()
	}
	requests := make([]domain.ScriptExchange, 0, len(group))
	for _, member := range group {
		session := s.skillSession(member.live)
		if session.ChannelID != inst.channel || session.Character.MapID != mapID || session.Character.HP <= 0 || s.eventOf(session.CharacterID) != inst {
			return domain.ErrStaleOwner
		}
		requests = append(requests, domain.ScriptExchange{Session: session,
			OperationID: "event-" + inst.id + "-" + action, Logs: []domain.ScriptLogChange{change}})
	}
	results, err := repo.ExecuteEventLogs(ctx, requests)
	if err != nil {
		return err
	}
	for i, result := range results {
		value := result.Character
		// A roster log changes the revision without changing the newer live vitals.
		value.HP, value.MP = requests[i].Session.Character.HP, requests[i].Session.Character.MP
		s.applyInventoryResult(group[i].live, value)
	}
	return nil
}
