package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
)

// Runtime-only authority, without a repository or catalog. Any unintended SQL,
// packet publication or content load fails instead of being a mock success.
func qaSnapshotFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, qaMobSnapshotRequest) {
	t.Helper()
	lease := domain.WorldLease{WorldID: 0, InstanceID: "snapshot-world", Epoch: 141, ExpiresAt: time.Now().Add(time.Minute)}
	session := domain.CharacterSession{CharacterID: 4910, AccountID: 7, SessionID: "private-authenticated-session", Generation: 8, ChannelID: 5, Lease: lease,
		Character: domain.Character{ID: 4910, AccountID: 7, WorldID: 0, MapID: domain.QAMobMap, X: -235, Y: 215, HP: 3220}}
	live, sink := &liveCharacter{session: session}, &gameplayTestSink{}
	s := &Server{cfg: platform.Config{WorldID: 0, RPCToken: strings.Repeat("t", 32)}, lease: lease,
		gameplay: &gameplaySpace{bootID: strings.Repeat("a", 32), rooms: map[mapKey]*gameplayRoom{}}}
	s.ready.Store(true)
	seedBoundScene(t, s, live, sink)
	visit := s.scenes[session.CharacterID]
	m := &gameplayMember{live: live, sink: sink, sessionID: session.SessionID, generation: session.Generation, worldID: 0, visit: visit}
	owner := session
	owner.Character = domain.Character{MapID: domain.QAMobMap}
	batch := "gm-native-original-spawn-batch"
	marker := &qaMobOwner{owner: owner, member: m, batch: batch, expires: time.Now().Add(time.Minute)}
	s.gameplay.rooms[mapKey{5, domain.QAMobMap}] = &gameplayRoom{members: map[int64]*gameplayMember{4910: m}, mobs: map[int32]*gameplayMob{
		1000001: {objectID: 1000001, stats: content.Mob{ID: 9300306, HP: 550000000}, hp: 550000000, life: content.Life{X: -76, Y: 214, Foothold: 39}, controller: 4910, qaOwner: marker},
		1000002: {objectID: 1000002, stats: content.Mob{ID: 9300306, HP: 550000000}, hp: 550000000, life: content.Life{X: -44, Y: 214, Foothold: 39}, controller: 4910, qaOwner: marker},
	}}
	return s, live, sink, qaMobSnapshotRequest{WorldID: 0, WorldEpoch: 141, ChannelID: 5, MapID: domain.QAMobMap, OwnerCharacterID: 4910, Batch: batch, ObjectIDs: []int32{1000002, 1000001}}
}

func qaSnapshotCall(t *testing.T, s *Server, q qaMobSnapshotRequest) (*httptest.ResponseRecorder, qaMobSnapshotResponse) {
	t.Helper()
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/qa-mobs/snapshot", bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer "+s.cfg.RPCToken)
	w := httptest.NewRecorder()
	s.qaMobSnapshot(w, r)
	var response qaMobSnapshotResponse
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return w, response
}

func qaSnapshotExpect(v qaMobSnapshotResponse) *qaMobSnapshotExpected {
	expect := &qaMobSnapshotExpected{BootHash: v.BootHash, VisitHash: v.VisitHash}
	for _, m := range v.Mobs {
		expect.Spawns = append(expect.Spawns, qaMobSnapshotSpawn{m.ObjectID, m.SpawnHash})
	}
	return expect
}

func TestQAMobSnapshotBaselineAndExpectedBirthHPBookends(t *testing.T) {
	s, live, sink, q := qaSnapshotFixture(t)
	before := time.Now()
	w, first := qaSnapshotCall(t, s, q)
	if w.Code != 200 || !first.Complete || !first.RuntimeOnly || !first.EvidenceEligible || first.WorldEpoch != 141 || first.ChannelID != 5 || first.OwnerGeneration != 8 || first.ObservedAt.Before(before) || first.ObservedAt.After(time.Now()) || len(first.Mobs) != 2 {
		t.Fatalf("baseline failed %d: %s", w.Code, w.Body)
	}
	if first.Mobs[0].ObjectID != 1000001 || first.Mobs[0].HP != 550000000 || first.Mobs[0].X != -76 || first.Mobs[0].Y != 214 || first.Mobs[0].Foothold != 39 || first.Mobs[0].ServerController != 4910 {
		t.Fatalf("not actual runtime fields: %+v", first.Mobs)
	}
	wire := w.Body.String()
	for _, secret := range []string{live.session.SessionID, live.session.Lease.InstanceID, s.scenes[4910].token, q.Batch, s.gameplay.bootID} {
		if strings.Contains(wire, secret) {
			t.Fatal("raw private identity leaked")
		}
	}
	q.Expected = qaSnapshotExpect(first)
	_, same := qaSnapshotCall(t, s, q)
	if !reflect.DeepEqual(first.Mobs, same.Mobs) || first.VisitHash != same.VisitHash {
		t.Fatal("unchanged runtime bookends differ")
	}
	g := s.gameplay
	g.mu.Lock()
	g.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001].hp -= 520
	g.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001].life.X = -80
	g.mu.Unlock()
	_, after := qaSnapshotCall(t, s, q)
	if !after.Complete || first.Mobs[0].HP-after.Mobs[0].HP != 520 || after.Mobs[0].X != -80 || first.Mobs[0].HP != 550000000 || first.Mobs[0].X != -76 || len(sink.packets) != 0 {
		t.Fatal("copy aliased live state or snapshot wrote packets")
	}
	// Snapshot SpawnHash must join the existing actual-damage journal exactly.
	if first.Mobs[0].SpawnHash != combatEvidenceHash(fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, 5, domain.QAMobMap, 1000001)) {
		t.Fatal("snapshot invented a different combat birth identity")
	}
}

func TestQAMobSnapshotHTTPRejectsBeforeRuntimeAccess(t *testing.T) {
	valid := `{"world_id":0,"world_epoch":141,"channel_id":5,"map_id":180000001,"owner_character_id":4910,"batch":"gm-native-original-spawn-batch","object_ids":[1000001]}`
	for _, tc := range []struct {
		name, method, remote, token, body string
		want                              int
	}{
		{"wrong-token", "POST", "127.0.0.1:1", "wrong", valid, 401},
		{"public-peer", "POST", "203.0.113.8:1", strings.Repeat("t", 32), valid, 401},
		{"missing-peer", "POST", "", strings.Repeat("t", 32), valid, 401},
		{"get", "GET", "127.0.0.1:1", strings.Repeat("t", 32), valid, 405},
		{"unknown-key", "POST", "127.0.0.1:1", strings.Repeat("t", 32), strings.TrimSuffix(valid, "}") + `,"sql":"no"}`, 400},
		{"trailing-json", "POST", "127.0.0.1:1", strings.Repeat("t", 32), valid + `{}`, 400},
		{"oversized", "POST", "127.0.0.1:1", strings.Repeat("t", 32), strings.Replace(valid, "gm-native-original-spawn-batch", strings.Repeat("x", 8192), 1), 400},
		{"null", "POST", "127.0.0.1:1", strings.Repeat("t", 32), `null`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: platform.Config{RPCToken: strings.Repeat("t", 32)}}
			r := httptest.NewRequest(tc.method, "/admin/qa-mobs/snapshot", strings.NewReader(tc.body))
			r.RemoteAddr = tc.remote
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			s.qaMobSnapshot(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), `"mobs"`) || strings.Contains(w.Body.String(), `"complete":true`) {
				t.Fatalf("unexpected response %d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestQAMobSnapshotStrictRequestBoundsAndExpectedSets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*qaMobSnapshotRequest)
	}{
		{"duplicate-oid", func(q *qaMobSnapshotRequest) { q.ObjectIDs = []int32{1000001, 1000001} }},
		{"empty", func(q *qaMobSnapshotRequest) { q.ObjectIDs = nil }},
		{"too-many", func(q *qaMobSnapshotRequest) { q.ObjectIDs = make([]int32, 13) }},
		{"negative", func(q *qaMobSnapshotRequest) { q.ObjectIDs[0] = -1 }},
		{"zero", func(q *qaMobSnapshotRequest) { q.ObjectIDs[0] = 0 }},
		{"other-map", func(q *qaMobSnapshotRequest) { q.MapID = 100000000 }},
		{"native-channel", func(q *qaMobSnapshotRequest) { q.ChannelID = 0 }},
		{"no-epoch", func(q *qaMobSnapshotRequest) { q.WorldEpoch = 0 }},
		{"missing-expected-spawn", func(q *qaMobSnapshotRequest) { q.Expected.Spawns = q.Expected.Spawns[:1] }},
		{"duplicate-expected-spawn", func(q *qaMobSnapshotRequest) { q.Expected.Spawns[0] = q.Expected.Spawns[1] }},
		{"different-expected-set", func(q *qaMobSnapshotRequest) { q.Expected.Spawns[0].ObjectID = 9 }},
		{"bad-hash", func(q *qaMobSnapshotRequest) { q.Expected.VisitHash = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, q := qaSnapshotFixture(t)
			_, first := qaSnapshotCall(t, s, q)
			q.Expected = qaSnapshotExpect(first)
			tc.change(&q)
			w, _ := qaSnapshotCall(t, s, q)
			if w.Code != 400 {
				t.Fatalf("invalid request accepted: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestQAMobSnapshotAllOrNothingCurrentOwnerAndLifetime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Server, *liveCharacter, *qaMobSnapshotRequest)
	}{
		{"not-ready", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) { s.ready.Store(false) }},
		{"expired-lease", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.lease.ExpiresAt = time.Now().Add(-time.Second)
		}},
		{"other-epoch", func(_ *Server, _ *liveCharacter, q *qaMobSnapshotRequest) { q.WorldEpoch++ }},
		{"other-world", func(_ *Server, _ *liveCharacter, q *qaMobSnapshotRequest) { q.WorldID++ }},
		{"other-channel", func(_ *Server, _ *liveCharacter, q *qaMobSnapshotRequest) { q.ChannelID = 4 }},
		{"lease-replaced", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) { s.lease.InstanceID = "replacement-world" }},
		{"missing-second", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			delete(s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs, 1000002)
		}},
		{"ordinary-second", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner = nil
		}},
		{"retired", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner.retired = true
		}},
		{"expired", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner.expires = time.Now().Add(-time.Second)
		}},
		{"other-batch", func(_ *Server, _ *liveCharacter, q *qaMobSnapshotRequest) { q.Batch += "-other" }},
		{"other-session", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner.owner.SessionID += "new"
		}},
		{"other-generation", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner.owner.Generation++
		}},
		{"other-account", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000002].qaOwner.owner.AccountID++
		}},
		{"member-identity", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].members[4910].sessionID += "new"
		}},
		{"missing-member-visit", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.rooms[mapKey{5, domain.QAMobMap}].members[4910].visit = nil
		}},
		{"empty-visit-token", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.scenes[4910].token = ""
		}},
		{"invalid-boot-token", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.bootID = "unreadable"
		}},
		{"same-oid-new-boot", func(s *Server, _ *liveCharacter, _ *qaMobSnapshotRequest) {
			s.gameplay.bootID = strings.Repeat("b", 32)
		}},
		{"wrong-expected-spawn", func(_ *Server, _ *liveCharacter, q *qaMobSnapshotRequest) {
			q.Expected.Spawns[1].SpawnHash = strings.Repeat("c", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, live, _, q := qaSnapshotFixture(t)
			_, first := qaSnapshotCall(t, s, q)
			q.Expected = qaSnapshotExpect(first)
			tc.change(s, live, &q)
			w, result := qaSnapshotCall(t, s, q)
			if w.Code != 409 || result.Complete || len(result.Mobs) != 0 || strings.Contains(w.Body.String(), `"mobs"`) {
				t.Fatalf("stale/partial snapshot released: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestQAMobSnapshotReentryCannotReadPriorMemberEvenWithoutExpected(t *testing.T) {
	s, live, sink, q := qaSnapshotFixture(t)
	_, first := qaSnapshotCall(t, s, q)
	old := s.scenes[4910]
	next := makeSceneVisit(live.session, live, sink)
	if next == old || next.token == old.token {
		t.Fatal("new entry reused visit")
	}
	s.installScene(next, live.session)
	room := s.gameplay.rooms[mapKey{5, domain.QAMobMap}]
	previousMember := room.members[4910]
	copyMember := *previousMember
	copyMember.visit = next
	room.members[4910] = &copyMember
	w, _ := qaSnapshotCall(t, s, q)
	if w.Code != 409 {
		t.Fatal("baseline borrowed old QA member")
	}
	// Even if the marker is associated with the new member, a comparison is
	// pinned to the old visit hash and must not treat the same OID as continuity.
	for _, mob := range room.mobs {
		mob.qaOwner.member = &copyMember
	}
	q.Expected = qaSnapshotExpect(first)
	w, _ = qaSnapshotCall(t, s, q)
	if w.Code != 409 {
		t.Fatal("expected visit accepted same-map reentry")
	}
}

func TestQAMobSnapshotQueuedBehindSceneChangeRejectsResolvedOldVisit(t *testing.T) {
	s, live, sink, q := qaSnapshotFixture(t)
	unlock, err := s.lockScenePublication(4910)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := s.snapshotQAMobs(q); done <- e }()
	deadline := time.Now().Add(time.Second)
	for {
		s.sceneMu.Lock()
		waiting := s.sceneGates[4910] != nil && s.sceneGates[4910].refs == 2
		s.sceneMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			unlock()
			t.Fatal("snapshot did not queue at existing scene gate")
		}
		time.Sleep(time.Millisecond)
	}
	if !s.gameplay.mu.TryLock() {
		unlock()
		t.Fatal("snapshot waited for scene gate while holding map lock")
	}
	s.gameplay.mu.Unlock()
	s.installScene(makeSceneVisit(live.session, live, sink), live.session)
	unlock()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("queued snapshot published resolved old visit")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot failed to release scene gate")
	}
}

func TestQAMobSnapshotIneligibleFlagsAreObservedWithoutMutationOrWait(t *testing.T) {
	s, live, _, q := qaSnapshotFixture(t)
	mob := s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001]
	mob.claiming, mob.qaHeld = true, true
	mob.reservation = &combatTicket{}
	mob.qaHeldUntil = time.Now().Add(-time.Minute) // Do not expire the hold here.
	mob.hp = 0
	live.persistMu.Lock() // Read does not join or wait for the durable operation.
	defer live.persistMu.Unlock()
	done := make(chan qaMobSnapshotResponse, 1)
	go func() { v, _ := s.snapshotQAMobs(q); done <- v }()
	select {
	case v := <-done:
		if !v.Complete || v.EvidenceEligible || !v.Mobs[0].Claiming || !v.Mobs[0].Reserved || !v.Mobs[0].Held || !v.Mobs[0].Defeated || v.Mobs[0].HP != 0 || !reflect.DeepEqual(v.Mobs[0].IneligibleReasons, []string{"claiming", "reserved", "held", "defeated"}) {
			t.Fatalf("ineligible values were hidden: %+v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot waited for persistMu/transaction")
	}
	if !mob.qaHeld || !mob.claiming || mob.reservation == nil || mob.hp != 0 {
		t.Fatal("diagnostic repaired runtime state")
	}
}

func TestQAMobSnapshotInvalidHPIsNotNormalized(t *testing.T) {
	for _, hp := range []int64{-1, 550000001} {
		t.Run(fmt.Sprint(hp), func(t *testing.T) {
			s, _, _, q := qaSnapshotFixture(t)
			mob := s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001]
			mob.hp = hp
			w, v := qaSnapshotCall(t, s, q)
			if w.Code != 200 || !v.Complete || v.EvidenceEligible || v.Mobs[0].EvidenceEligible || v.Mobs[0].HP != hp || mob.hp != hp {
				t.Fatalf("invalid HP normalized or accepted: %d %+v", w.Code, v)
			}
			found := false
			for _, reason := range v.Mobs[0].IneligibleReasons {
				found = found || reason == "invalid_hp"
			}
			if !found {
				t.Fatal("invalid HP lacked explicit reason")
			}
		})
	}
}

func TestQAMobSnapshotExpiryUsesLockedObservationTime(t *testing.T) {
	s, _, _, q := qaSnapshotFixture(t)
	visit := s.scenes[4910]
	marker := s.gameplay.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001].qaOwner
	marker.expires = time.Now().Add(time.Minute)
	s.gameplay.mu.Lock()
	_, err := s.gameplay.copyQAMobSnapshotLocked(q, visit, marker.expires)
	s.gameplay.mu.Unlock()
	if err == nil {
		t.Fatal("expiry equality accepted")
	}
}

func TestQAMobSnapshotExpiresWhileWaitingForMapLock(t *testing.T) {
	s, _, _, q := qaSnapshotFixture(t)
	g := s.gameplay
	g.mu.Lock()
	locked := true
	defer func() {
		if locked {
			g.mu.Unlock()
		}
	}()
	marker := g.rooms[mapKey{5, domain.QAMobMap}].mobs[1000001].qaOwner
	marker.expires = time.Now().Add(150 * time.Millisecond)
	expires := marker.expires
	done := make(chan error, 1)
	go func() { _, err := s.snapshotQAMobs(q); done <- err }()
	// The snapshot resolves the scene and owns its gate before trying g.mu.
	// Keep g.mu held past expiry; now must be sampled after that wait.
	deadline := time.Now().Add(time.Second)
	for {
		s.sceneMu.Lock()
		gate := s.sceneGates[4910]
		holding := false
		if gate != nil {
			holding = !gate.mu.TryLock()
			if !holding {
				gate.mu.Unlock()
			}
		}
		s.sceneMu.Unlock()
		if holding {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot did not reach scene gate")
		}
		time.Sleep(time.Millisecond)
	}
	if !time.Now().Before(expires) {
		t.Fatal("fixture did not enter before expiry")
	}
	<-time.After(time.Until(expires.Add(3 * time.Millisecond)))
	g.mu.Unlock()
	locked = false
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("snapshot borrowed pre-lock expiry time")
		}
	case <-time.After(time.Second):
		t.Fatal("expired snapshot remained blocked")
	}
}

type qaSnapshotSlowWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *qaSnapshotSlowWriter) Header() http.Header { return w.header }
func (*qaSnapshotSlowWriter) WriteHeader(int)       {}
func (w *qaSnapshotSlowWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func TestQAMobSnapshotNoSceneOrMapLockAcrossHTTPWrite(t *testing.T) {
	s, _, _, q := qaSnapshotFixture(t)
	b, _ := json.Marshal(q)
	r := httptest.NewRequest("POST", "/admin/qa-mobs/snapshot", bytes.NewReader(b))
	r.RemoteAddr = "[::1]:1234"
	r.Header.Set("Authorization", "Bearer "+s.cfg.RPCToken)
	w := &qaSnapshotSlowWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { s.qaMobSnapshot(w, r); close(done) }()
	defer func() {
		close(w.release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP writer goroutine leaked after release")
		}
	}()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("response did not reach writer")
	}
	if !s.gameplay.mu.TryLock() {
		t.Fatal("room lock retained during HTTP write")
	}
	s.gameplay.mu.Unlock()
	gateFree := make(chan struct{})
	go func() { unlock, _ := s.lockScenePublication(4910); unlock(); close(gateFree) }()
	select {
	case <-gateFree:
	case <-time.After(time.Second):
		t.Fatal("scene gate retained during HTTP write")
	}
}

func TestQAMobSnapshotAcceptsPharaohMap(t *testing.T) {
	_, _, _, q := qaSnapshotFixture(t)
	q.MapID = domain.QAPharaohMap
	if !q.valid() {
		t.Fatal("qaMobSnapshotRequest rejected QAPharaohMap")
	}
	q.MapID = 100000000
	if q.valid() {
		t.Fatal("qaMobSnapshotRequest accepted non-QA map 100000000")
	}
}
