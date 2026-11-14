package world

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"maple-go-server/scripts"
)

// scriptReloadReport is what one reload did. A rejected file keeps the
// version that was already running.
type scriptReloadReport struct {
	Changed  []string          `json:"changed"`
	Removed  []string          `json:"removed"`
	Rejected map[string]string `json:"rejected"`
}

// reloadScripts reads the configured script directory again and swaps in
// every source that changed and still compiles. A conversation that is open
// keeps the source it started with; the next one gets the new source.
func (s *Server) reloadScripts() (scriptReloadReport, error) {
	report := scriptReloadReport{Changed: []string{}, Removed: []string{}, Rejected: map[string]string{}}
	if s.scripts == nil || s.cfg.ScriptsDir == "" {
		return report, errors.New("no script directory configured")
	}
	s.scripts.reload.Lock()
	defer s.scripts.reload.Unlock()
	engine := s.scripts.engine
	update, err := scripts.Prepare(s.cfg.ScriptsDir, engine.Check)
	if err != nil {
		return report, err
	}
	// New sources are registered before Source reports them and removed ones
	// are forgotten after it stops, so a script that Source finds can start.
	for name, source := range update.Changed {
		if _, err := engine.Register(name, source); err != nil {
			return report, err
		}
		report.Changed = append(report.Changed, name)
	}
	update.Commit()
	for _, name := range update.Removed {
		engine.Remove(name)
	}
	sort.Strings(report.Changed)
	report.Removed = append(report.Removed, update.Removed...)
	report.Rejected = update.Rejected
	return report, nil
}

// scriptReload is the operator's reload switch on the loopback health
// listener. It takes no body: the directory comes from the configuration.
func (s *Server) scriptReload(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if s.scripts == nil || s.cfg.ScriptsDir == "" {
		debugJSON(w, 409, map[string]string{"error": "scripts_dir_not_configured"})
		return
	}
	report, err := s.reloadScripts()
	if err != nil {
		slog.Error("script_reload_failed", "dir", s.cfg.ScriptsDir, "error", err)
		debugJSON(w, 500, map[string]string{"error": "reload_failed", "detail": err.Error()})
		return
	}
	slog.Info("scripts_reloaded", "changed", report.Changed, "removed", report.Removed, "rejected", report.Rejected)
	debugJSON(w, 200, report)
}
