package world

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func buffScopePlayer(id int64, x int, buffs ...domain.SkillBuff) *liveCharacter {
	return &liveCharacter{session: domain.CharacterSession{CharacterID: id, AccountID: id, SessionID: fmt.Sprintf("buff-%d", id), Generation: 1, ChannelID: 1,
		Character: domain.Character{ID: id, Name: fmt.Sprintf("Buff%d", id), Level: 10, MapID: 40000, X: x, HP: 50, MaxHP: 50, MP: 30, MaxMP: 30, Revision: 1, SkillBuffs: buffs}}}
}

func enterBuffScope(t *testing.T, s *Server, live *liveCharacter, sink packetSink) {
	t.Helper()
	live.mu.Lock()
	v := live.session
	live.mu.Unlock()
	s.maps.Enter(v.ChannelID, v.Character.MapID, v.Character, sink)
	if err := s.enterSkills(context.Background(), v.ChannelID, live, sink); err != nil {
		t.Fatal(err)
	}
}

func buffPackets(sink *gameplayTestSink) [][]byte {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	out := make([][]byte, len(sink.packets))
	for i, p := range sink.packets {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

func assertRemoteBuffAfterSpawn(t *testing.T, packets [][]byte, source int64, bit int, speed *byte) {
	t.Helper()
	spawned := false
	for _, p := range packets {
		if len(p) < 6 {
			continue
		}
		op := binary.LittleEndian.Uint16(p)
		if int64(binary.LittleEndian.Uint32(p[2:6])) != source {
			continue
		}
		if op == protocol.SendSpawnPlayer {
			spawned = true
		}
		if op != 0x145 {
			continue
		}
		if !spawned {
			t.Fatal("remote state preceded its remote player spawn")
		}
		length := 43
		if speed != nil {
			length++
		}
		if len(p) != length {
			t.Fatalf("native remote buff width=%d want%d", len(p), length)
		}
		for word := 0; word < 8; word++ {
			want := uint32(0)
			if word == 7-bit/32 {
				want = 1 << uint(bit%32)
			}
			if binary.LittleEndian.Uint32(p[6+word*4:]) != want {
				t.Fatalf("remote mask word%d", word)
			}
		}
		if speed != nil && p[38] != *speed {
			t.Fatal("native remote speed value shifted")
		}
		return
	}
	t.Fatal("no active remote buff followed the spawn")
}

func TestEveryImplementedBuffEnterReentryAndAOISpawn(t *testing.T) {
	for _, tc := range []struct {
		id    int
		stat  string
		bit   int
		speed *byte
	}{
		{1001, "RECOVERY", 34, nil}, {1002, "SPEED", 7, func() *byte { v := byte(10); return &v }()}, {1001003, "WDEF", 1, nil},
	} {
		t.Run(tc.stat, func(t *testing.T) {
			s := &Server{skills: newSkillSpace(), maps: newMapSpace(100)}
			buff := domain.SkillBuff{ID: tc.id, Level: 1, Stat: tc.stat, Value: 10, ExpiresAt: time.Now().Add(time.Minute)}
			a, b := buffScopePlayer(1, 0, buff), buffScopePlayer(2, 20)
			first, second := &gameplayTestSink{}, &gameplayTestSink{}
			enterBuffScope(t, s, a, first)
			enterBuffScope(t, s, b, second)
			assertRemoteBuffAfterSpawn(t, buffPackets(second), 1, tc.bit, tc.speed)
			if second.count(0x144) != 0 {
				t.Fatal("late entry replayed transient cast FX")
			}
			// AOI leave produces RemovePlayer; returning produces a new remote
			// object with empty defaults, immediately followed by current state.
			b.mu.Lock()
			b.session.Character.X = 500
			look := b.session.Character
			b.mu.Unlock()
			s.maps.Move(1, 40000, 2, look, 0, 0, nil)
			before := len(buffPackets(second))
			b.mu.Lock()
			b.session.Character.X = 20
			look = b.session.Character
			b.mu.Unlock()
			s.maps.Move(1, 40000, 2, look, 0, 0, nil)
			assertRemoteBuffAfterSpawn(t, buffPackets(second)[before:], 1, tc.bit, tc.speed)
			if second.count(0x144) != 0 {
				t.Fatal("AOI reentry replayed transient cast FX")
			}
			// Same object/sink/map, distinct visit: state must be restored again.
			s.maps.Leave(1, 40000, 2, second)
			before = len(buffPackets(second))
			enterBuffScope(t, s, b, second)
			assertRemoteBuffAfterSpawn(t, buffPackets(second)[before:], 1, tc.bit, tc.speed)
			// Expired state must not be reintroduced by a later spawn.
			a.mu.Lock()
			a.session.Character.SkillBuffs[0].ExpiresAt = time.Now().Add(-time.Second)
			a.mu.Unlock()
			s.maps.Leave(1, 40000, 2, second)
			before = second.count(0x145)
			enterBuffScope(t, s, b, second)
			if second.count(0x145) != before {
				t.Fatal("expired state restored on reentry")
			}
		})
	}
}

func TestSkillPublicationRejectsOldVisitAfterRemoveAndChannelTransfer(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100)}
	buff := domain.SkillBuff{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}
	a, b := buffScopePlayer(1, 0, buff), buffScopePlayer(2, 20)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	enterBuffScope(t, s, b, second)
	source := s.skillMemberFor(a.session, a, first)
	oldPeers := s.skillPeers(1, 1, 40000)
	oldSnapshot := cloneCombatSession(a.session)
	s.maps.Leave(1, 40000, 1, first)
	before := second.count(0x145)
	_ = s.publishSkillCast(source, oldPeers, oldSnapshot, domain.SkillResult{}, protocol.SkillCast{SkillID: 1002, Level: 1})
	if second.count(0x145) != before || second.count(0x144) != 0 {
		t.Fatal("stale removed-source event reached peer")
	}
	// Transfer returns on a new sink and generation. The old source token must
	// remain invalid even after the same character ID exists in the same map.
	s.leaveSkills(1, a)
	a.mu.Lock()
	a.session.Generation++
	a.session.ChannelID = 2
	a.mu.Unlock()
	newSink := &gameplayTestSink{}
	enterBuffScope(t, s, a, newSink)
	_ = s.publishSkillCast(source, oldPeers, oldSnapshot, domain.SkillResult{}, protocol.SkillCast{SkillID: 1002, Level: 1})
	if second.count(0x145) != before || newSink.count(0x154) != 0 {
		t.Fatal("old generation crossed channel/connection scope")
	}
}

func TestOldSkillCancellationCannotClearRecastStat(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100)}
	old := domain.SkillBuff{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}
	a, b := buffScopePlayer(1, 0, old), buffScopePlayer(2, 20)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	enterBuffScope(t, s, b, second)
	snapshot := cloneCombatSession(a.session)
	a.mu.Lock()
	a.session.Character.SkillBuffs[0].ExpiresAt = old.ExpiresAt.Add(time.Minute)
	a.session.Character.Revision++
	a.mu.Unlock()
	s.publishSkillTick(snapshot, first, domain.SkillResult{Expired: []domain.SkillBuff{old}}, 50)
	if first.count(0x2e) != 0 || second.count(0x146) != 0 {
		t.Fatal("old cancellation removed newer active stat")
	}
}

func TestConcurrentBuffCastsAndWarpsDoNotAcquirePeerPersistenceLocks(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
	lives := []*liveCharacter{buffScopePlayer(1, 0), buffScopePlayer(2, 20)}
	sinks := []*gameplayTestSink{{}, {}}
	for i := range lives {
		enterBuffScope(t, s, lives[i], sinks[i])
	}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		entered <- struct{}{}
		<-release
		c := q.Session.Character
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}}
		return domain.SkillResult{Character: c, Changed: true}, nil
	}}
	var wg sync.WaitGroup
	for i := range lives {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.castSkill(context.Background(), lives[i], sinks[i], protocol.SkillCast{SkillID: 1002, Level: 1, Tick: uint32(i + 1)})
		}(i)
	}
	<-entered
	<-entered
	for i := range lives {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			live := lives[i]
			live.persistMu.Lock()
			defer live.persistMu.Unlock()
			s.maps.Leave(1, 40000, live.session.CharacterID, sinks[i])
			live.mu.Lock()
			live.session.Character.MapID = 50000
			look := live.session.Character
			live.mu.Unlock()
			s.maps.Enter(1, 50000, look, sinks[i])
			_ = s.enterSkills(context.Background(), 1, live, sinks[i])
		}(i)
	}
	close(release)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("opposite casts/warps deadlocked through peer persistence locks")
	}
}

func TestEntryDuringCastCommitReceivesStateWithoutHistoricalCastFX(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
	a, b := buffScopePlayer(1, 0), buffScopePlayer(2, 20)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	entered, release := make(chan struct{}), make(chan struct{})
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		close(entered)
		<-release
		c := q.Session.Character
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}}
		return domain.SkillResult{Character: c, Changed: true}, nil
	}}
	done := make(chan struct{})
	go func() {
		_ = s.castSkill(context.Background(), a, first, protocol.SkillCast{SkillID: 1002, Level: 1, Tick: 9})
		close(done)
	}()
	<-entered
	// A's database call owns no publication gate, so B can complete entry.
	enterBuffScope(t, s, b, second)
	if second.count(0x145) != 0 {
		t.Fatal("buff was published before commit")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("entry/cast publication deadlocked")
	}
	if second.count(0x145) != 1 || second.count(0x144) != 0 {
		t.Fatal("entry/commit window lost state or replayed historical cast FX")
	}
}

func TestAOIEntryAfterCastAudienceGetsStateWithoutCastFlash(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100)}
	a, b := buffScopePlayer(1, 0), buffScopePlayer(2, 500)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	enterBuffScope(t, s, b, second)
	source := s.skillMemberFor(a.session, a, first)
	audience := s.skillCastAudience(source)
	if len(audience) != 0 {
		t.Fatal("out-of-AOI peer captured as cast witness")
	}
	a.mu.Lock()
	a.session.Character.SkillBuffs = []domain.SkillBuff{{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}}
	snapshot := cloneCombatSession(a.session)
	a.mu.Unlock()
	b.mu.Lock()
	b.session.Character.X = 20
	look := b.session.Character
	b.mu.Unlock()
	s.maps.Move(1, 40000, 2, look, 0, 0, nil)
	_ = s.publishSkillCast(source, audience, snapshot, domain.SkillResult{}, protocol.SkillCast{SkillID: 1002, Level: 1})
	if second.count(0x145) == 0 || second.count(0x144) != 0 {
		t.Fatal("AOI late entry lost state or received historical flash")
	}
}

func TestToggleRecastSendsLocalAndRemoteCancel(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
	on := domain.SkillBuff{ID: 2111007, Level: 10, Stat: "TELEPORT_MASTERY", Value: 20, ExpiresAt: time.Now().Add(time.Hour)}
	a, b := buffScopePlayer(1, 0, on), buffScopePlayer(2, 20)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	enterBuffScope(t, s, b, second)
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		c := q.Session.Character
		c.Revision++
		c.SkillBuffs = nil
		return domain.SkillResult{Character: c, Changed: true, Expired: []domain.SkillBuff{on}}, nil
	}}
	if err := s.castSkill(context.Background(), a, first, protocol.SkillCast{SkillID: 2111007, Level: 10, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if first.count(0x2e) != 1 || second.count(0x146) != 1 {
		t.Fatalf("toggle-off cancels local=%d remote=%d", first.count(0x2e), second.count(0x146))
	}
}
