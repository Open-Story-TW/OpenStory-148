package world

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// gmCommandAdmin runs one "!" command as an online character, exactly as if the
// character had typed it: the same grant check, audit row and feedback packet.
// Automated tests use it where the client cannot type. It only exists when
// MAPLEGO_QA_ADMIN_COMMANDS=1 and needs the RPC token.
func (s *Server) gmCommandAdmin(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req struct {
		CharacterID int64  `json:"character_id"`
		Text        string `json:"text"`
	}
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.CharacterID < 1 || !strings.HasPrefix(req.Text, "!") {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	s.mu.RLock()
	v := s.scenes[req.CharacterID]
	s.mu.RUnlock()
	if v == nil || v.live == nil || v.sink == nil {
		debugJSON(w, 404, map[string]string{"error": "character_offline"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	tap := &gmTap{}
	gmTaps.Store(v.sink, tap)
	handled, err := s.handleGMChat(ctx, v.live, v.sink, req.Text)
	gmTaps.Delete(v.sink)
	tap.mu.Lock()
	out := map[string]any{"handled": handled, "feedback": tap.lines}
	tap.mu.Unlock()
	if err != nil {
		out["error"] = err.Error()
	}
	debugJSON(w, 200, out)
}

// gmTaps collects the feedback lines of the command an admin call is running,
// keyed by the character's connection; the lines still go to the client.
var gmTaps sync.Map

type gmTap struct {
	mu    sync.Mutex
	lines []string
}

func gmFeedbackTap(sink packetSink, message string) {
	if v, ok := gmTaps.Load(sink); ok {
		t := v.(*gmTap)
		t.mu.Lock()
		t.lines = append(t.lines, message)
		t.mu.Unlock()
	}
}

func qaAdminCommandsEnabled() bool { return os.Getenv("MAPLEGO_QA_ADMIN_COMMANDS") == "1" }
