package world

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func sceneFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *mapEntryQueue) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	for _, dir := range []string{"Effect.wz", "Map.wz/Map/Map0", "Map.wz/Map/Map1"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"Effect.wz/Direction3.img.xml":      `<imgdir name="Direction3.img"><imgdir name="goLith"><imgdir name="Scene0"><imgdir name="0"><int name="type" value="2"/><int name="start" value="5000"/><int name="field" value="104000000"/></imgdir></imgdir></imgdir></imgdir>`,
		"Map.wz/Map/Map0/002010000.img.xml": `<imgdir name="002010000.img"><imgdir name="info"><string name="onUserEnter" value="goLith"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="0"/><int name="y" value="0"/></imgdir></imgdir></imgdir>`,
		"Map.wz/Map/Map1/104000000.img.xml": `<imgdir name="104000000.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="10"/><int name="y" value="20"/></imgdir></imgdir></imgdir>`,
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	g.catalog, err = content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	scripts, err := newScriptWorld()
	if err != nil {
		t.Fatal(err)
	}
	live := &liveCharacter{session: domain.CharacterSession{AccountID: 1, CharacterID: 7, SessionID: "scene-owned-session", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 7, MapID: 2010000, Gender: 0, HP: 50, MaxHP: 50, Revision: 1}}}
	s := &Server{gameplay: g, scripts: scripts, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{7: live.session}}
	sink := &gameplayTestSink{}
	seedBoundScene(t, s, live, sink)
	return s, live, sink, mapQueue(live)
}
func sceneRequest(live *liveCharacter, sink packetSink, token uint64) mapEntryRequest {
	return mapEntryRequest{token: token, session: live.session, channel: 1, sink: sink, name: "map/on_user_enter/goLith.lua"}
}
func TestSceneTransferNeedsExactUnexpiredOwnedTicket(t *testing.T) {
	for _, name := range []string{"wrong-target", "early", "expired", "wrong-generation", "wheel", "dead", "wrong-sink"} {
		t.Run(name, func(t *testing.T) {
			s, live, sink, q := sceneFixture(t)
			q.token = 1
			r := sceneRequest(live, sink, 1)
			ticket := &sceneTicket{request: r, scene: content.MapScene{Transition: content.SceneTransition{TargetMap: 104000000}}, notBefore: time.Now().Add(-time.Second), deadline: time.Now().Add(time.Minute)}
			q.scene = ticket
			request := protocol.PortalRequest{TargetMap: 104000000, Revival: &protocol.RevivalRequest{}}
			var actual packetSink = sink
			switch name {
			case "wrong-target":
				request.TargetMap = 10000
			case "early":
				ticket.notBefore = time.Now().Add(time.Minute)
			case "expired":
				ticket.deadline = time.Now().Add(-time.Second)
			case "wrong-generation":
				live.session.Generation++
			case "wheel":
				request.Revival.WheelType = 1
			case "dead":
				live.session.Character.HP = 0
			case "wrong-sink":
				actual = &gameplayTestSink{}
			}
			handled, err := s.sceneTransfer(context.Background(), 1, live, actual, request)
			// A dead character's request falls through to the death dialog's
			// revival (which validates it); everything else is consumed.
			if handled != (name != "dead") || err != nil || ticket.claimed || live.session.Character.MapID != 2010000 {
				t.Fatalf("handled=%v err=%v ticket=%+v", handled, err, ticket)
			}
		})
	}
}
func TestSceneTransferWarpsOnceAndRejectsReplay(t *testing.T) {
	s, live, sink, q := sceneFixture(t)
	var saves atomic.Int32
	s.repo = gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error {
		saves.Add(1)
		if c.MapID != 104000000 {
			t.Fatalf("map%d", c.MapID)
		}
		return nil
	}}
	q.token = 1
	r := sceneRequest(live, sink, 1)
	q.scene = &sceneTicket{request: r, scene: content.MapScene{Transition: content.SceneTransition{TargetMap: 104000000}}, notBefore: time.Now().Add(-time.Second), deadline: time.Now().Add(time.Minute)}
	req := protocol.PortalRequest{TargetMap: 104000000, Revival: &protocol.RevivalRequest{}}
	if handled, err := s.sceneTransfer(context.Background(), 1, live, sink, req); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if live.session.Character.MapID != 104000000 || saves.Load() != 1 || sink.count(protocol.SendSetField) != 1 {
		t.Fatal("scene did not perform one durable warp")
	}
	if err := s.portalGameplay(context.Background(), 1, live, sink, req); err != nil {
		t.Fatal(err)
	}
	if saves.Load() != 1 {
		t.Fatal("replayed scene changed durable state")
	}
}
func TestQueuedMapEntryCannotCancelNewNPC(t *testing.T) {
	s, live, sink, q := sceneFixture(t)
	id := scripting.Identity{SessionID: live.session.SessionID, CharacterID: 7, Generation: 3}
	old := &worldScript{live: live, identity: id}
	old.mu.Lock()
	r := sceneRequest(live, sink, 1)
	r.oldScript = old
	q.token = 1
	q.pending = &r
	q.running = true
	done := make(chan struct{})
	go func() { s.runMapEntries(live, q); close(done) }()
	newNPC := &worldScript{live: live, identity: id, kind: "npc"}
	s.scripts.mu.Lock()
	s.scripts.active[7] = newNPC
	s.scripts.mu.Unlock()
	old.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("map worker remained blocked")
	}
	s.scripts.mu.Lock()
	current := s.scripts.active[7]
	s.scripts.mu.Unlock()
	if current != newNPC {
		t.Fatal("stale worker replaced current NPC")
	}
}

func TestMapEntryQueuesOutsideOldLuaLockAndStartsMountedScene(t *testing.T) {
	s, live, sink, q := sceneFixture(t)
	id := scripting.Identity{SessionID: live.session.SessionID, CharacterID: 7, Generation: 3}
	old := &worldScript{live: live, identity: id}
	old.mu.Lock()
	s.scripts.active[7] = old
	enqueued := make(chan struct{})
	go func() { s.enqueueMapEntry(1, live, sink); close(enqueued) }()
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		old.mu.Unlock()
		t.Fatal("warp enqueue waited for the caller's Lua lock")
	}
	old.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		running := q.running
		ticket := q.scene
		q.mu.Unlock()
		if !running {
			if ticket == nil || ticket.scene.Transition.TargetMap != 104000000 || ticket.scene.Transition.After != 5*time.Second {
				t.Fatalf("scene ticket %+v", ticket)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scene worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	defer s.invalidateMapEntry(live)
	if sink.count(0x166) != 1 || sink.count(0x165) != 1 || sink.count(0x154) != 1 {
		t.Fatal("missing native scene packets")
	}
}

func TestSceneClaimRevalidatesAfterWaitingForPersistence(t *testing.T) {
	s, live, sink, q := sceneFixture(t)
	var saves atomic.Int32
	s.repo = gameplayTestRepo{save: func(domain.CharacterSession, domain.Character) error { saves.Add(1); return nil }}
	q.token = 1
	r := sceneRequest(live, sink, 1)
	ticket := &sceneTicket{request: r, scene: content.MapScene{Transition: content.SceneTransition{TargetMap: 104000000}}, notBefore: time.Now().Add(-time.Second), deadline: time.Now().Add(time.Minute)}
	q.scene = ticket
	live.persistMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.sceneTransfer(context.Background(), 1, live, sink, protocol.PortalRequest{TargetMap: 104000000, Revival: &protocol.RevivalRequest{}})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		claimed := ticket.claimed
		q.mu.Unlock()
		if claimed {
			break
		}
		if time.Now().After(deadline) {
			live.persistMu.Unlock()
			t.Fatal("scene did not reach persistence wait")
		}
		time.Sleep(time.Millisecond)
	}
	// A different already-authorized transition won the persistence lock.
	live.mu.Lock()
	invalidateMapEntryQueue(q)
	live.session.Character.MapID = 10000
	live.mu.Unlock()
	seedBoundScene(t, s, live, sink)
	live.persistMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("scene remained blocked")
	}
	if saves.Load() != 0 || live.session.Character.MapID != 10000 || sink.count(0x165) != 0 || sink.count(0x166) != 0 {
		t.Fatal("stale scene changed or unlocked the replacement map")
	}
}

func TestMapEntryCannotOutlivePublishedLoginOwner(t *testing.T) {
	s, live, sink, q := sceneFixture(t)
	q.token = 1
	r := sceneRequest(live, sink, 1)
	if !s.mapEntryCurrent(live, q, r) {
		t.Fatal("current map entry rejected")
	}
	replacement := live.session
	replacement.Generation++
	replacement.SessionID = "replacement-map-session"
	s.mu.Lock()
	s.online[live.session.CharacterID] = replacement
	s.mu.Unlock()
	if s.mapEntryCurrent(live, q, r) {
		t.Fatal("old live pointer bypassed published replacement owner")
	}
}

type gatedWarpSink struct {
	gameplayTestSink
	once             sync.Once
	entered, release chan struct{}
}

func (s *gatedWarpSink) WritePacket(packet []byte) error {
	if len(packet) >= 2 && binary.LittleEndian.Uint16(packet) == protocol.SendSetField {
		s.once.Do(func() { close(s.entered); <-s.release })
	}
	return s.gameplayTestSink.WritePacket(packet)
}
func TestCommittedWarpPublishesBeforeAnotherWarpCanCommit(t *testing.T) {
	s, live, _, _ := sceneFixture(t)
	s.scripts = nil
	sink := &gatedWarpSink{entered: make(chan struct{}), release: make(chan struct{})}
	seedBoundScene(t, s, live, sink)
	var saves atomic.Int32
	secondSaved := make(chan struct{})
	s.repo = gameplayTestRepo{save: func(domain.CharacterSession, domain.Character) error {
		if saves.Add(1) == 2 {
			close(secondSaved)
		}
		return nil
	}}
	first, _ := s.gameplay.catalog.Map(104000000)
	firstEntry, _ := first.Spawn()
	second, _ := s.gameplay.catalog.Map(10000)
	secondEntry, _ := second.Spawn()
	firstDone := make(chan error, 1)
	go func() { firstDone <- s.warpGameplay(context.Background(), 1, live, sink, first.ID, firstEntry) }()
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("first warp did not reach field publication")
	}
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- s.warpGameplay(context.Background(), 1, live, sink, second.ID, secondEntry)
	}()
	<-secondStarted
	select {
	case <-secondSaved:
		close(sink.release)
		t.Fatal("second warp committed before first field publication")
	case <-time.After(30 * time.Millisecond):
	}
	close(sink.release)
	for _, done := range []chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("warp remained blocked")
		}
	}
	if saves.Load() != 2 || live.session.Character.MapID != second.ID || sink.count(protocol.SendSetField) != 2 {
		t.Fatal("warp order or final destination invalid")
	}
}
