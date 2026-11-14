package world

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"maple-go-server/internal/domain"
)

// The monitor lets the web admin watch a world without the world depending on
// it. The world keeps recent events in memory and answers reads on its
// loopback health listener; it never calls the admin, writes nothing to the
// database for it, and loses nothing but old events if nobody is reading.

const monitorRingSize = 4096

// MonitorEvent is one chat line or social action.
type MonitorEvent struct {
	Seq         uint64    `json:"seq"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	Channel     int       `json:"channel"`
	MapID       int       `json:"map_id"`
	CharacterID int64     `json:"character_id"`
	Name        string    `json:"name"`
	Text        string    `json:"text"`
}

// monitorRing holds the newest events. record takes one short lock and never
// blocks on a reader.
type monitorRing struct {
	mu     sync.Mutex
	next   uint64
	events [monitorRingSize]MonitorEvent
}

var monitor monitorRing

func (r *monitorRing) record(kind string, session domain.CharacterSession, text string) {
	if len(text) > 512 {
		text = text[:512]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	event := MonitorEvent{At: time.Now().UTC(), Kind: kind, Channel: session.ChannelID, MapID: session.Character.MapID,
		CharacterID: session.CharacterID, Name: session.Character.Name, Text: text}
	r.mu.Lock()
	r.next++
	event.Seq = r.next
	r.events[event.Seq%monitorRingSize] = event
	r.mu.Unlock()
}

// since returns up to limit events after seq, oldest first, and whether older
// ones were already overwritten.
func (r *monitorRing) since(seq uint64, limit int) (events []MonitorEvent, last uint64, lost bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	last = r.next
	if seq > last {
		seq = 0 // The reader's position is from an earlier process.
	}
	first := seq + 1
	if last >= monitorRingSize && first < last-monitorRingSize+1 {
		first, lost = last-monitorRingSize+1, seq != 0
	}
	for at := first; at <= last && len(events) < limit; at++ {
		events = append(events, r.events[at%monitorRingSize])
	}
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	return events, last, lost
}

func recordChat(kind string, session domain.CharacterSession, text string) {
	monitor.record(kind, session, text)
}

func (s *Server) monitorAuthorized(w http.ResponseWriter, r *http.Request, method string) bool {
	if len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return false
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return false
	}
	return true
}

func (s *Server) monitorEvents(w http.ResponseWriter, r *http.Request) {
	if !s.monitorAuthorized(w, r, http.MethodGet) {
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	events, last, lost := monitor.since(after, 1000)
	if events == nil {
		events = []MonitorEvent{}
	}
	debugJSON(w, 200, map[string]any{"instance": s.cfg.InstanceID, "last": last, "lost": lost, "events": events})
}

// MonitorPlayer is a character as the world holds it in memory right now.
type MonitorPlayer struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
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

func monitorPlayer(c domain.Character, channel int) MonitorPlayer {
	return MonitorPlayer{ID: c.ID, Name: c.Name, Channel: channel, MapID: c.MapID, X: c.X, Y: c.Y, Level: c.Level, Job: c.Job,
		HP: c.HP, MaxHP: c.MaxHP, MP: c.MP, MaxMP: c.MaxMP, EXP: c.EXP, Mesos: c.Mesos, GM: c.GM != 0}
}

func (s *Server) monitorPlayers(w http.ResponseWriter, r *http.Request) {
	if !s.monitorAuthorized(w, r, http.MethodGet) {
		return
	}
	// Session snapshots carry stats; map rooms carry where a character is.
	s.mu.RLock()
	players := make(map[int64]MonitorPlayer, len(s.online))
	for id, session := range s.online {
		players[id] = monitorPlayer(session.Character, session.ChannelID)
	}
	s.mu.RUnlock()
	if s.maps != nil {
		s.maps.mu.Lock()
		for key, room := range s.maps.rooms {
			for id, member := range room {
				p, known := players[id]
				if !known {
					continue
				}
				p.Channel, p.MapID, p.X, p.Y, p.InField = key.channel, key.mapID, member.x, member.y, true
				players[id] = p
			}
		}
		s.maps.mu.Unlock()
	}
	out := make([]MonitorPlayer, 0, len(players))
	for _, p := range players {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	debugJSON(w, 200, map[string]any{"instance": s.cfg.InstanceID, "world_id": s.wcfg.ID, "players": out})
}

// monitorNotice sends a yellow notice to everyone in this world, one channel
// or one map of a channel. Only characters standing in a map receive it.
func (s *Server) monitorNotice(w http.ResponseWriter, r *http.Request) {
	if !s.monitorAuthorized(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req struct {
		Channel int    `json:"channel"` // 0 is every channel
		MapID   int    `json:"map_id"`  // 0 is every map
		Message string `json:"message"`
	}
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len(req.Message) > 255 || strings.ContainsAny(req.Message, "\x00\r\n") || req.Channel < 0 || req.MapID < 0 {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	debugJSON(w, 200, map[string]int{"delivered": s.broadcastNotice(req.Channel, req.MapID, req.Message)})
}
