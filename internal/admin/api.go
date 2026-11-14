package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/settings"
)

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"email": actor(r), "mail_configured": s.mail.configured()})
}

type serviceState struct {
	Name     string            `json:"name"`
	Label    string            `json:"label"`
	Status   string            `json:"status"`
	PID      int               `json:"pid,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
}

// services asks each game service's loopback health endpoint how it is and
// which settings it is running with.
func (s *Server) services(ctx context.Context) []serviceState {
	out := []serviceState{{Name: "login", Label: "登入伺服器"}}
	urls := []string{s.serviceURL("login", 0)}
	for _, world := range s.cfg.Worlds {
		out = append(out, serviceState{Name: "world" + strconv.Itoa(world.ID), Label: "世界 " + world.Name})
		urls = append(urls, s.serviceURL("world", world.ID))
	}
	out = append(out, serviceState{Name: "cashshop", Label: "購物商城"})
	urls = append(urls, s.serviceURL("cashshop", 0))
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Status = "stopped"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, urls[i], nil)
			if err != nil {
				return
			}
			res, err := s.client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			var body struct {
				Status  string `json:"status"`
				PID     int    `json:"pid"`
				Details struct {
					Settings map[string]string `json:"settings"`
				} `json:"details"`
			}
			if json.NewDecoder(res.Body).Decode(&body) != nil {
				out[i].Status = "unknown"
				return
			}
			out[i].Status, out[i].PID, out[i].Settings = body.Status, body.PID, body.Details.Settings
		}()
	}
	wg.Wait()
	return out
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Saved(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "讀取設定失敗")
		return
	}
	values := settings.Resolve(saved)
	services := s.services(r.Context())
	// A setting that is not live waits for a restart while any running
	// service still has the old value.
	pending := map[string]bool{}
	for _, def := range settings.Defs() {
		if def.Live {
			continue
		}
		for _, service := range services {
			if active, ok := service.Settings[def.Key]; ok && active != values[def.Key] {
				pending[def.Key] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"defs": settings.Defs(), "values": values, "pending": pending})
}

func (s *Server) postSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Values map[string]string `json:"values"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Values) == 0 {
		writeError(w, http.StatusBadRequest, "沒有要儲存的設定")
		return
	}
	for key, value := range req.Values {
		if _, err := settings.Normalize(key, value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := settings.Save(r.Context(), s.db, actor(r), req.Values); err != nil {
		writeError(w, http.StatusInternalServerError, "儲存設定失敗")
		return
	}
	slog.Info("admin_settings_saved", "actor", actor(r), "values", req.Values)
	s.getSettings(w, r)
}

type channelCount struct {
	WorldID   int `json:"world_id"`
	ChannelID int `json:"channel_id"`
	Players   int `json:"players"`
}

// onlineSessions selects sessions held by a world process whose lease is alive.
const onlineSessions = `SELECT a.character_id,a.world_id,COALESCE(o.channel_id,0) AS channel_id
 FROM ops_account_sessions a
 JOIN ops_runtime_leases l ON l.world_id=a.world_id AND l.instance_id=a.owner_instance AND l.epoch=a.world_epoch AND l.expires_at>clock_timestamp()
 LEFT JOIN ops_character_ownership o ON o.character_id=a.character_id
 WHERE a.state='world' AND a.character_id>0`

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT world_id,channel_id,count(*) FROM (`+onlineSessions+`) online GROUP BY 1,2 ORDER BY 1,2`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "讀取狀態失敗")
		return
	}
	defer rows.Close()
	counts := []channelCount{}
	online := 0
	for rows.Next() {
		var c channelCount
		if err = rows.Scan(&c.WorldID, &c.ChannelID, &c.Players); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取狀態失敗")
			return
		}
		online += c.Players
		counts = append(counts, c)
	}
	var accounts, characters int
	_ = s.db.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM auth_accounts),(SELECT count(*) FROM game_characters)`).Scan(&accounts, &characters)
	worlds := make([]map[string]any, 0, len(s.cfg.Worlds))
	for _, wc := range s.cfg.Worlds {
		worlds = append(worlds, map[string]any{"id": wc.ID, "name": wc.Name, "channel_count": wc.ChannelCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": s.services(r.Context()), "worlds": worlds, "channels": counts,
		"online": online, "accounts": accounts, "characters": characters})
}

type player struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Account   string `json:"account"`
	AccountID int64  `json:"account_id"`
	WorldID   int    `json:"world_id"`
	ChannelID int    `json:"channel_id"`
	Level     int    `json:"level"`
	Job       int    `json:"job"`
	MapID     int    `json:"map_id"`
	GM        bool   `json:"gm"`
	Online    bool   `json:"online"`
}

// players lists who is online, or with ?q= finds characters by name.
func (s *Server) players(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	const columns = `SELECT c.id,c.name,acc.name,c.account_id,c.world_id,COALESCE(online.channel_id,0),c.level,c.job,c.map_id,c.gm<>0,online.character_id IS NOT NULL
 FROM game_characters c JOIN auth_accounts acc ON acc.id=c.account_id `
	sqlText := columns + `JOIN (` + onlineSessions + `) online ON online.character_id=c.id ORDER BY c.world_id,c.name LIMIT 500`
	if r.URL.Query().Get("all") == "1" {
		// Everyone, online first. The saved copy of an online character can
		// be a little behind the world that holds it.
		sqlText = columns + `LEFT JOIN (` + onlineSessions + `) online ON online.character_id=c.id
 ORDER BY online.character_id IS NULL,c.world_id,c.name LIMIT 500`
	}
	var args []any
	if query != "" {
		if len(query) > 24 {
			writeError(w, http.StatusBadRequest, "搜尋文字太長")
			return
		}
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		sqlText = columns + `LEFT JOIN (` + onlineSessions + `) online ON online.character_id=c.id
 WHERE c.name ILIKE $1 OR acc.name ILIKE $1 ORDER BY c.name LIMIT 200`
		args = append(args, "%"+escaped+"%")
	}
	rows, err := s.db.QueryContext(r.Context(), sqlText, args...)
	if err != nil {
		slog.Error("admin_players_query", "error", err)
		writeError(w, http.StatusInternalServerError, "讀取玩家失敗")
		return
	}
	defer rows.Close()
	out := []player{}
	for rows.Next() {
		var p player
		if err = rows.Scan(&p.ID, &p.Name, &p.Account, &p.AccountID, &p.WorldID, &p.ChannelID, &p.Level, &p.Job, &p.MapID, &p.GM, &p.Online); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取玩家失敗")
			return
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"players": out})
}

type gmGrant struct {
	CharacterID int64     `json:"character_id"`
	Name        string    `json:"name"`
	Account     string    `json:"account"`
	WorldID     int       `json:"world_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	Permanent   bool      `json:"permanent"`
	Enabled     bool      `json:"enabled"`
	By          string    `json:"authorized_by"`
	Reason      string    `json:"reason"`
}

func (s *Server) gmList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT g.character_id,c.name,acc.name,g.world_id,g.expires_at,g.enabled AND g.expires_at>clock_timestamp(),g.authorized_by,g.reason
 FROM ops_qa_gm_grants g JOIN game_characters c ON c.id=g.character_id JOIN auth_accounts acc ON acc.id=g.account_id ORDER BY g.updated_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "讀取 GM 授權失敗")
		return
	}
	defer rows.Close()
	out := []gmGrant{}
	for rows.Next() {
		var g gmGrant
		if err = rows.Scan(&g.CharacterID, &g.Name, &g.Account, &g.WorldID, &g.ExpiresAt, &g.Enabled, &g.By, &g.Reason); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取 GM 授權失敗")
			return
		}
		g.Permanent = !g.ExpiresAt.Before(domain.QAGMPermanentExpiry)
		out = append(out, g)
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": out})
}

func (s *Server) gmChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Character string `json:"character"`
		Action    string `json:"action"`
		Hours     int    `json:"hours"`
		Permanent bool   `json:"permanent"`
		Reason    string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	repo, ok := s.repo.(domain.GMRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "這個資料庫不支援 GM 授權")
		return
	}
	if req.Action != "grant" && req.Action != "revoke" || req.Action == "grant" && !req.Permanent && (req.Hours < 1 || req.Hours > 24) {
		writeError(w, http.StatusBadRequest, "授權時數必須是 1 到 24")
		return
	}
	grant := domain.QAGMGrantRequest{OperationID: "admin-" + platform.NewID(), Actor: actor(r), Action: req.Action, Role: domain.QAGMRole,
		// A timed grant is at most 24 hours in the store; the margin absorbs clock skew.
		ExpiresAt: time.Now().Add(time.Duration(max(req.Hours, 1))*time.Hour - time.Minute), Reason: strings.TrimSpace(req.Reason),
		Permanent: req.Action == "grant" && req.Permanent}
	if grant.Reason == "" {
		grant.Reason = "web admin"
	}
	name := strings.TrimSpace(req.Character)
	err := s.db.QueryRowContext(r.Context(), `SELECT id,account_id,world_id FROM game_characters WHERE name_key=lower($1)`, name).Scan(&grant.CharacterID, &grant.AccountID, &grant.WorldID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到這個角色")
		return
	}
	if _, err = repo.AuthorizeQAGM(r.Context(), grant); err != nil {
		status, message := http.StatusInternalServerError, "授權失敗"
		switch {
		case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyOnline):
			status, message = http.StatusConflict, "這個帳號必須先登出遊戲才能變更 GM 授權"
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = http.StatusBadRequest, "授權內容不正確"
		case errors.Is(err, domain.ErrForbidden):
			status, message = http.StatusForbidden, "這個角色不能變更 GM 授權"
		case errors.Is(err, domain.ErrNotFound):
			status, message = http.StatusNotFound, "找不到這個角色"
		}
		slog.Warn("admin_gm_change_failed", "actor", actor(r), "character", name, "action", req.Action, "error", err)
		writeError(w, status, message)
		return
	}
	slog.Info("admin_gm_change", "actor", actor(r), "character", name, "action", req.Action, "hours", req.Hours, "permanent", grant.Permanent)
	s.gmList(w, r)
}

func (s *Server) adminList(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Email   string `json:"email"`
		Builtin bool   `json:"builtin"`
	}
	out := []entry{}
	seen := map[string]bool{}
	for email := range s.bootstrap {
		out = append(out, entry{email, true})
		seen[email] = true
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT email FROM ops_admin_users ORDER BY created_at`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "讀取管理員失敗")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取管理員失敗")
			return
		}
		if !seen[email] {
			out = append(out, entry{email, false})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out, "me": actor(r)})
}

func (s *Server) adminChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email  string `json:"email"`
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "請輸入正確的 email")
		return
	}
	var err error
	switch req.Action {
	case "add":
		_, err = s.db.ExecContext(r.Context(), `INSERT INTO ops_admin_users(email,added_by) VALUES($1,$2) ON CONFLICT DO NOTHING`, email, actor(r))
	case "remove":
		if s.bootstrap[email] {
			writeError(w, http.StatusConflict, "這個管理員寫在服務器的啟動設定裡，要從那裡移除")
			return
		}
		if email == actor(r) {
			writeError(w, http.StatusConflict, "不能移除自己")
			return
		}
		// Removing the row also ends every session of that address.
		if _, err = s.db.ExecContext(r.Context(), `DELETE FROM ops_admin_users WHERE email=$1`, email); err == nil {
			_, err = s.db.ExecContext(r.Context(), `DELETE FROM ops_admin_sessions WHERE email=$1`, email)
		}
	default:
		writeError(w, http.StatusBadRequest, "不支援的操作")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "變更管理員失敗")
		return
	}
	slog.Info("admin_user_change", "actor", actor(r), "email", email, "action", req.Action)
	s.adminList(w, r)
}
