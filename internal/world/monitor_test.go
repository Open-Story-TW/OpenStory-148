package world

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
)

func TestMonitorRingKeepsNewestAndReportsLoss(t *testing.T) {
	var ring monitorRing
	session := domain.CharacterSession{CharacterID: 7, ChannelID: 2, Character: domain.Character{Name: "楓", MapID: 100}}
	if events, last, lost := ring.since(0, 10); len(events) != 0 || last != 0 || lost {
		t.Fatalf("empty ring: %v %d %v", events, last, lost)
	}
	for i := 0; i < 3; i++ {
		ring.record("chat", session, "hi")
	}
	events, last, lost := ring.since(1, 10)
	if len(events) != 2 || events[0].Seq != 2 || last != 3 || lost || events[0].Name != "楓" || events[0].Channel != 2 || events[0].MapID != 100 {
		t.Fatalf("after 1: %+v last=%d lost=%v", events, last, lost)
	}
	// A page is bounded and resumes from its own last sequence.
	if events, last, _ = ring.since(0, 2); len(events) != 2 || last != 2 {
		t.Fatalf("page: %d events, last=%d", len(events), last)
	}
	// A reader position from an earlier process restarts at the beginning.
	if events, _, lost = ring.since(99, 10); len(events) != 3 || lost {
		t.Fatalf("stale position: %d events lost=%v", len(events), lost)
	}
	for i := 0; i < monitorRingSize+5; i++ {
		ring.record("chat", session, "x")
	}
	events, last, lost = ring.since(3, monitorRingSize*2)
	if len(events) != monitorRingSize || !lost || last != uint64(monitorRingSize+8) || events[0].Seq != 9 {
		t.Fatalf("overwritten: %d events first=%d last=%d lost=%v", len(events), events[0].Seq, last, lost)
	}
}

func TestMonitorRingTruncatesOnACharacterBoundary(t *testing.T) {
	var ring monitorRing
	ring.record("chat", domain.CharacterSession{}, strings.Repeat("楓", 400))
	events, _, _ := ring.since(0, 1)
	if got := events[0].Text; len(got) > 512 || len(got)%3 != 0 || got != strings.Repeat("楓", len(got)/3) {
		t.Fatalf("bad truncation: %d bytes", len(got))
	}
}

type monitorSink struct{ packets [][]byte }

func (m *monitorSink) WritePacket(p []byte) error { m.packets = append(m.packets, p); return nil }

func monitorTestServer() (*Server, map[string]*monitorSink) {
	s := &Server{online: map[int64]domain.CharacterSession{}, maps: newMapSpace(0)}
	s.cfg.RPCToken = strings.Repeat("t", 32)
	sinks := map[string]*monitorSink{}
	for id, at := range map[int64][2]int{1: {1, 100}, 2: {1, 200}, 3: {2, 100}} {
		c := domain.Character{ID: id, Name: string(rune('A' + id)), MapID: at[1], Level: int(id) * 10, HP: 5, MaxHP: 9}
		sink := &monitorSink{}
		sinks[c.Name] = sink
		s.online[id] = domain.CharacterSession{CharacterID: id, ChannelID: at[0], Character: c}
		s.maps.Enter(at[0], at[1], c, sink)
	}
	// Between maps: online, but in no room.
	s.online[4] = domain.CharacterSession{CharacterID: 4, ChannelID: 3, Character: domain.Character{ID: 4, Name: "E", MapID: 300}}
	return s, sinks
}

func monitorCall(t *testing.T, handler http.HandlerFunc, method, target, body, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestMonitorNoticeScopes(t *testing.T) {
	s, _ := monitorTestServer()
	token := s.cfg.RPCToken
	for _, tc := range []struct {
		body      string
		delivered float64
	}{
		{`{"channel":0,"map_id":0,"message":"all"}`, 3},
		{`{"channel":1,"map_id":0,"message":"channel"}`, 2},
		{`{"channel":1,"map_id":100,"message":"map"}`, 1},
		{`{"channel":0,"map_id":100,"message":"map everywhere"}`, 2},
		{`{"channel":9,"map_id":0,"message":"nobody"}`, 0},
	} {
		code, out := monitorCall(t, s.monitorNotice, http.MethodPost, "/admin/monitor/notice", tc.body, token)
		if code != 200 || out["delivered"] != tc.delivered {
			t.Errorf("%s: code=%d out=%v", tc.body, code, out)
		}
	}
	for _, body := range []string{`{"message":""}`, `{"message":"a\nb"}`, `{"channel":-1,"message":"x"}`, `{"message":"x","extra":1}`, `not json`} {
		if code, _ := monitorCall(t, s.monitorNotice, http.MethodPost, "/admin/monitor/notice", body, token); code != 400 {
			t.Errorf("%s: code=%d", body, code)
		}
	}
	if code, _ := monitorCall(t, s.monitorNotice, http.MethodPost, "/admin/monitor/notice", `{"message":"x"}`, "wrong"); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := monitorCall(t, s.monitorNotice, http.MethodGet, "/admin/monitor/notice", "", token); code != 405 {
		t.Errorf("GET: %d", code)
	}
}

func TestMonitorPlayersMergesStatsAndPosition(t *testing.T) {
	s, _ := monitorTestServer()
	code, out := monitorCall(t, s.monitorPlayers, http.MethodGet, "/admin/monitor/players", "", s.cfg.RPCToken)
	players, _ := out["players"].([]any)
	if code != 200 || len(players) != 4 {
		t.Fatalf("code=%d players=%v", code, out)
	}
	first, last := players[0].(map[string]any), players[3].(map[string]any)
	if first["name"] != "B" || first["level"] != 10.0 || first["channel"] != 1.0 || first["map_id"] != 100.0 || first["in_field"] != true || first["max_hp"] != 9.0 {
		t.Errorf("in a map: %v", first)
	}
	if last["name"] != "E" || last["in_field"] != false || last["map_id"] != 300.0 {
		t.Errorf("between maps: %v", last)
	}
	if code, _ = monitorCall(t, s.monitorPlayers, http.MethodGet, "/admin/monitor/players", "", ""); code != 401 {
		t.Errorf("no token: %d", code)
	}
}
