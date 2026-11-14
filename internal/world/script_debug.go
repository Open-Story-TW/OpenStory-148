package world

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"maple-go-server/internal/scripting"
)

type scriptDebugRequest struct {
	Action      string `json:"action"`
	CharacterID int64  `json:"character_id"`
	SessionID   string `json:"session_id"`
	Generation  int64  `json:"generation"`
	SHA256      string `json:"sha256"`
	Lines       []int  `json:"lines"`
}

func debugJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// scriptDebugger operates existing conversations only. It exposes no Lua eval,
// local mutation, arbitrary script source, or force-start gameplay capability.
func (s *Server) scriptDebugger(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req scriptDebugRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || len(req.Lines) > 256 {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if s.scripts == nil {
		debugJSON(w, 503, map[string]string{"error": "scripts_unavailable"})
		return
	}
	if req.Action == "list" {
		s.mu.RLock()
		online := make([]any, 0, len(s.online))
		for _, session := range s.online {
			online = append(online, map[string]any{"character_id": session.CharacterID, "session_id": session.SessionID, "generation": session.Generation, "name": session.Character.Name, "map_id": session.Character.MapID})
		}
		s.mu.RUnlock()
		s.scripts.mu.Lock()
		active := make([]*worldScript, 0, len(s.scripts.active))
		for _, a := range s.scripts.active {
			active = append(active, a)
		}
		s.scripts.mu.Unlock()
		rows := make([]any, 0, len(active))
		for _, a := range active {
			a.mu.Lock()
			if a.session != nil {
				event, err := a.session.CurrentEvent(a.identity)
				if err == nil {
					rows = append(rows, map[string]any{"identity": a.identity, "kind": a.kind, "npc_id": a.npcID, "event": event})
				}
			}
			a.mu.Unlock()
		}
		debugJSON(w, 200, map[string]any{"online": online, "scripts": rows})
		return
	}
	id := scripting.Identity{SessionID: req.SessionID, CharacterID: req.CharacterID, Generation: req.Generation}
	s.mu.RLock()
	online, ok := s.online[req.CharacterID]
	s.mu.RUnlock()
	if !ok || online.SessionID != req.SessionID || online.Generation != req.Generation {
		debugJSON(w, 409, map[string]string{"error": "stale_character_session"})
		return
	}
	if req.Action == "arm" {
		for _, line := range req.Lines {
			if line < 1 || line > 100000 {
				debugJSON(w, 400, map[string]string{"error": "invalid_line"})
				return
			}
		}
		s.scripts.mu.Lock()
		s.scripts.armed[req.CharacterID] = scriptDebugArm{identity: id, options: scripting.StartOptions{Debug: true, Breakpoints: append([]int(nil), req.Lines...)}}
		s.scripts.mu.Unlock()
		debugJSON(w, 200, map[string]bool{"armed": true})
		return
	}
	s.scripts.mu.Lock()
	a := s.scripts.active[req.CharacterID]
	s.scripts.mu.Unlock()
	if a == nil {
		debugJSON(w, 404, map[string]string{"error": "no_active_script"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.identity != id || a.session == nil {
		debugJSON(w, 409, map[string]string{"error": "stale_script_session"})
		return
	}
	event, err := a.session.CurrentEvent(id)
	if err != nil || req.SHA256 == "" || event.SHA256 != req.SHA256 {
		debugJSON(w, 409, map[string]string{"error": "stale_script_hash"})
		return
	}
	switch req.Action {
	case "source":
		var source string
		source, err = a.session.Source(id)
		if err == nil {
			debugJSON(w, 200, map[string]any{"script": event.Script, "sha256": event.SHA256, "source": source})
			return
		}
	case "inspect":
		var snapshot scripting.DebugSnapshot
		snapshot, err = a.session.Inspect(id)
		if err == nil {
			debugJSON(w, 200, snapshot)
			return
		}
	case "step":
		event, err = a.session.Step(r.Context(), id)
	case "continue":
		event, err = a.session.Continue(r.Context(), id)
	case "pause":
		a.session.RequestPause()
	case "breakpoints":
		err = a.session.SetBreakpoints(id, req.Lines)
	case "cancel":
		a.session.Cancel()
		a.server.scripts.mu.Lock()
		delete(a.server.scripts.active, id.CharacterID)
		a.server.scripts.mu.Unlock()
		debugJSON(w, 200, map[string]bool{"cancelled": true})
		return
	default:
		debugJSON(w, 400, map[string]string{"error": "unknown_action"})
		return
	}
	if err != nil {
		debugJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	if req.Action == "step" || req.Action == "continue" {
		if err = a.deliver(event); err != nil {
			debugJSON(w, 500, map[string]string{"error": "delivery_failed"})
			return
		}
	}
	debugJSON(w, 200, event)
}
