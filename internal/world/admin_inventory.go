package world

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The web admin's view of and edits to a character this world holds in
// memory. Like the monitor these answer on the loopback health listener with
// the operator token. An edit runs as one more durable command of that
// character: under its persist lock, fenced by its session, then published to
// its client.

// adminCharacter returns the live copy of an online character.
func (s *Server) adminCharacter(w http.ResponseWriter, r *http.Request) {
	if !s.monitorAuthorized(w, r, http.MethodGet) {
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	s.mu.RLock()
	session, ok := s.online[id]
	s.mu.RUnlock()
	if !ok {
		debugJSON(w, 404, map[string]string{"error": "not_online"})
		return
	}
	c := session.Character
	// The admin shows bags and balances; the rest would only be weight.
	c.Quests, c.Skills, c.SkillBuffs, c.SkillCooldowns = nil, nil, nil, nil
	debugJSON(w, 200, map[string]any{"instance": s.cfg.InstanceID, "channel": session.ChannelID, "character": c})
}

func (s *Server) adminInventory(w http.ResponseWriter, r *http.Request) {
	if !s.monitorAuthorized(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req domain.AdminInventoryRequest
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.CharacterID < 1 {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := s.applyAdminInventory(ctx, req)
	if err != nil {
		status, message := 500, "failed"
		switch {
		case errors.Is(err, errAdminNotOnline):
			status, message = 404, "not_online"
		case errors.Is(err, errAdminBusy), errors.Is(err, domain.ErrStaleOwner):
			status, message = 409, "busy"
		case errors.Is(err, domain.ErrAdminChanged):
			status, message = 409, "changed"
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = 400, "invalid_request"
		case errors.Is(err, domain.ErrNotFound):
			status, message = 404, "not_found"
		case errors.Is(err, domain.ErrInsufficient):
			status, message = 409, "insufficient"
		case errors.Is(err, domain.ErrForbidden):
			status, message = 403, "forbidden"
		default:
			slog.Error("admin_inventory_failed", "character", req.CharacterID, "kind", req.Kind, "operation", req.OperationID, "error", err)
		}
		debugJSON(w, status, map[string]string{"error": message})
		return
	}
	result.Character.Quests, result.Character.Skills, result.Character.SkillBuffs, result.Character.SkillCooldowns = nil, nil, nil, nil
	debugJSON(w, 200, result)
}

var (
	errAdminNotOnline = errors.New("character is not in this world")
	// errAdminBusy: the character is between maps or channels, or in a trade
	// or settlement that owns its bags for a moment.
	errAdminBusy = errors.New("character is busy")
)

func (s *Server) applyAdminInventory(ctx context.Context, req domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
	repo, ok := s.repo.(domain.AdminInventoryRepository)
	if !ok || s.skills == nil {
		return domain.AdminInventoryResult{}, errors.New("admin inventory unavailable")
	}
	s.skills.mu.Lock()
	m := s.skills.members[req.CharacterID]
	s.skills.mu.Unlock()
	if m == nil {
		s.mu.RLock()
		_, online := s.online[req.CharacterID]
		s.mu.RUnlock()
		if online {
			return domain.AdminInventoryResult{}, errAdminBusy
		}
		return domain.AdminInventoryResult{}, errAdminNotOnline
	}
	live, sink := m.live, m.sink
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := cloneCombatSession(s.skillSession(live))
	if session.CharacterID != req.CharacterID || s.skillMemberFor(session, live, sink) != m || s.gmBusy(live) {
		return domain.AdminInventoryResult{}, errAdminBusy
	}
	req.Session = &session
	result, err := repo.ApplyAdminInventory(ctx, req)
	if err != nil && !combatKnownFailure(err) && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrAlreadyOnline) && !errors.Is(err, domain.ErrAdminChanged) {
		// The commit may or may not have happened. The operation ID makes
		// asking again safe: a committed change is returned, not repeated.
		slog.Warn("admin_inventory_uncertain", "character", req.CharacterID, "operation", req.OperationID, "error", err)
		again, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		result, err = repo.ApplyAdminInventory(again, req)
		done()
	}
	if err != nil {
		return domain.AdminInventoryResult{}, err
	}
	if result.Character.ID != session.CharacterID || result.Character.AccountID != session.AccountID {
		return domain.AdminInventoryResult{}, domain.ErrStaleOwner
	}
	// A replay this world has already applied was also already shown.
	known := result.Replayed && result.Character.Revision <= session.Character.Revision
	s.applyInventoryResult(live, result.Character)
	if known {
		return result, nil
	}
	if len(result.Changes) > 0 {
		if packet := protocol.ModifyInventory(result.Changes); packet != nil {
			_ = sink.WritePacket(packet)
		}
	}
	if req.Kind == domain.AdminGiveMesos {
		if packet := protocol.UpdateMesos(result.Character.Mesos, true); packet != nil {
			_ = sink.WritePacket(packet)
		}
	}
	if result.Worn {
		s.refreshCharacterLook(live)
		if packet := protocol.InventoryVitals(result.Character); packet != nil {
			_ = sink.WritePacket(packet)
		}
	}
	slog.Info("admin_inventory", "actor", req.Actor, "character", req.CharacterID, "kind", req.Kind, "operation", req.OperationID)
	return result, nil
}
