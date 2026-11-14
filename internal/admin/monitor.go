package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/platform"
)

// The admin reads each world's loopback monitor endpoints. A world that is
// down or slow only makes its own part of a page empty; nothing here can hold
// a world up, because the worlds never call the admin.

const (
	eventPoll      = 2 * time.Second
	eventRetention = 30 * 24 * time.Hour
)

// world calls one monitor endpoint of a world and decodes its JSON reply.
func (s *Server) world(ctx context.Context, world platform.WorldConfig, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", 19081+world.ID*2, path), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.RPCToken)
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("world %d %s: status %d", world.ID, path, res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}

type monitorEvent struct {
	Seq         int64     `json:"seq"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	Channel     int       `json:"channel"`
	MapID       int       `json:"map_id"`
	CharacterID int64     `json:"character_id"`
	Name        string    `json:"name"`
	Text        string    `json:"text"`
}

// collect copies new events of every world into ops_admin_events until ctx
// ends. Events a world overwrote while the admin was not running are gone.
func (s *Server) collect(ctx context.Context) {
	ticker := time.NewTicker(eventPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, world := range s.cfg.Worlds {
				// Drain a backlog now instead of one page per tick.
				for more := true; more && ctx.Err() == nil; {
					var err error
					if more, err = s.collectWorld(ctx, world); err != nil {
						slog.Debug("admin_collect", "world", world.ID, "error", err)
					}
				}
			}
		}
	}
}

func (s *Server) collectWorld(ctx context.Context, world platform.WorldConfig) (more bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var instance string
	var seq int64
	if err = s.db.QueryRowContext(ctx, `SELECT instance,seq FROM ops_admin_event_cursors WHERE world_id=$1`, world.ID).Scan(&instance, &seq); err != nil {
		instance, seq = "", 0
	}
	var page struct {
		Instance string         `json:"instance"`
		Last     int64          `json:"last"`
		Lost     bool           `json:"lost"`
		Events   []monitorEvent `json:"events"`
	}
	after := seq
	if err = s.world(ctx, world, http.MethodGet, "/admin/monitor/events?after="+strconv.FormatInt(after, 10), nil, &page); err != nil {
		return false, err
	}
	if page.Instance != instance && after != 0 {
		// The world restarted; its sequence starts over.
		if _, err = s.db.ExecContext(ctx, `DELETE FROM ops_admin_event_cursors WHERE world_id=$1`, world.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	if page.Lost {
		slog.Warn("admin_events_lost", "world", world.ID, "after", after)
	}
	if len(page.Events) == 0 && page.Instance == instance {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, e := range page.Events {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ops_admin_events(world_id,instance,seq,at,kind,channel,map_id,character_id,name,body) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`,
			world.ID, page.Instance, e.Seq, e.At, e.Kind, e.Channel, e.MapID, e.CharacterID, e.Name, e.Text); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_admin_event_cursors(world_id,instance,seq) VALUES($1,$2,$3) ON CONFLICT(world_id) DO UPDATE SET instance=excluded.instance,seq=excluded.seq`, world.ID, page.Instance, page.Last); err != nil {
		return false, err
	}
	return len(page.Events) >= 1000, tx.Commit()
}

type livePlayer struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	WorldID int    `json:"world_id"`
	Channel int    `json:"channel"`
	MapID   int    `json:"map_id"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Level   int    `json:"level"`
	Job     int    `json:"job"`
	HP      int    `json:"hp"`
	MaxHP   int    `json:"max_hp"`
	MP      int    `json:"mp"`
	MaxMP   int    `json:"max_mp"`
	EXP     int64  `json:"exp"`
	Mesos   int64  `json:"mesos"`
	GM      bool   `json:"gm"`
	InField bool   `json:"in_field"`
}

// live is every world's in-memory view of who is online.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	type result struct {
		players []livePlayer
		up      bool
	}
	results := make([]result, len(s.cfg.Worlds))
	var wg sync.WaitGroup
	for i, world := range s.cfg.Worlds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var page struct {
				Players []livePlayer `json:"players"`
			}
			if s.world(r.Context(), world, http.MethodGet, "/admin/monitor/players", nil, &page) != nil {
				return
			}
			for j := range page.Players {
				page.Players[j].WorldID = world.ID
			}
			results[i] = result{page.Players, true}
		}()
	}
	wg.Wait()
	players := []livePlayer{}
	worlds := make([]map[string]any, 0, len(results))
	for i, world := range s.cfg.Worlds {
		players = append(players, results[i].players...)
		worlds = append(worlds, map[string]any{"id": world.ID, "name": world.Name, "up": results[i].up, "channel_count": world.ChannelCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"worlds": worlds, "players": players})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	where, args := []string{"true"}, []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	switch kind := query.Get("kind"); kind {
	case "":
	case "chat":
		where = append(where, "kind LIKE 'chat%'")
	case "party", "trade":
		add("kind=$%d", kind)
	default:
		writeError(w, http.StatusBadRequest, "不支援的記錄種類")
		return
	}
	if text := strings.TrimSpace(query.Get("q")); text != "" {
		if len(text) > 64 {
			writeError(w, http.StatusBadRequest, "搜尋文字太長")
			return
		}
		escaped := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
		args = append(args, escaped)
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR body ILIKE $%d)", len(args), len(args)))
	}
	if before, err := strconv.ParseInt(query.Get("before"), 10, 64); err == nil && before > 0 {
		add("id<$%d", before)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,world_id,at,kind,channel,map_id,character_id,name,body FROM ops_admin_events WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT 100`, args...)
	if err != nil {
		slog.Error("admin_events_query", "error", err)
		writeError(w, http.StatusInternalServerError, "讀取記錄失敗")
		return
	}
	defer rows.Close()
	type event struct {
		ID      int64 `json:"id"`
		WorldID int   `json:"world_id"`
		monitorEvent
	}
	out := []event{}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.ID, &e.WorldID, &e.At, &e.Kind, &e.Channel, &e.MapID, &e.CharacterID, &e.Name, &e.Text); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取記錄失敗")
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

type noticeRecord struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	WorldID   int       `json:"world_id"`
	Channel   int       `json:"channel"`
	MapID     int       `json:"map_id"`
	Message   string    `json:"message"`
	Delivered int       `json:"delivered"`
}

func (s *Server) noticeList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,at,actor,world_id,channel,map_id,message,delivered FROM ops_admin_notices ORDER BY id DESC LIMIT 50`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "讀取公告失敗")
		return
	}
	defer rows.Close()
	out := []noticeRecord{}
	for rows.Next() {
		var n noticeRecord
		if err = rows.Scan(&n.ID, &n.At, &n.Actor, &n.WorldID, &n.Channel, &n.MapID, &n.Message, &n.Delivered); err != nil {
			writeError(w, http.StatusInternalServerError, "讀取公告失敗")
			return
		}
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"notices": out})
}

// noticeSend delivers a notice to one world or, with world -1, to all.
func (s *Server) noticeSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		World   int    `json:"world"`
		Channel int    `json:"channel"`
		MapID   int    `json:"map_id"`
		Message string `json:"message"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len(req.Message) > 255 || strings.ContainsAny(req.Message, "\x00\r\n") {
		writeError(w, http.StatusBadRequest, "公告內容必須是 1 到 255 個位元組的單行文字")
		return
	}
	if req.Channel < 0 || req.MapID < 0 || req.World < -1 {
		writeError(w, http.StatusBadRequest, "公告範圍不正確")
		return
	}
	delivered, reached := 0, 0
	for _, world := range s.cfg.Worlds {
		if req.World != -1 && req.World != world.ID {
			continue
		}
		var reply struct {
			Delivered int `json:"delivered"`
		}
		body := map[string]any{"channel": req.Channel, "map_id": req.MapID, "message": req.Message}
		if err := s.world(r.Context(), world, http.MethodPost, "/admin/monitor/notice", body, &reply); err != nil {
			slog.Warn("admin_notice_failed", "world", world.ID, "error", err)
			continue
		}
		reached++
		delivered += reply.Delivered
	}
	if reached == 0 {
		writeError(w, http.StatusBadGateway, "世界服務沒有回應，公告沒有送出")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO ops_admin_notices(actor,world_id,channel,map_id,message,delivered) VALUES($1,$2,$3,$4,$5,$6)`, actor(r), req.World, req.Channel, req.MapID, req.Message, delivered); err != nil {
		slog.Error("admin_notice_record", "error", err)
	}
	slog.Info("admin_notice", "actor", actor(r), "world", req.World, "channel", req.Channel, "map", req.MapID, "delivered", delivered)
	writeJSON(w, http.StatusOK, map[string]any{"delivered": delivered})
}
