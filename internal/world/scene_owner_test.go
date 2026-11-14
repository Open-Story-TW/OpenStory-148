package world

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type sceneTestRepository struct {
	domain.Repository
	validate func(domain.CharacterSession) error
	save     func(domain.CharacterSession, domain.Character) error
}

func (r *sceneTestRepository) ValidateSceneOwner(_ context.Context, v domain.CharacterSession) error {
	return r.validate(v)
}
func (r *sceneTestRepository) SaveCharacter(_ context.Context, v domain.CharacterSession, c domain.Character) error {
	if r.save != nil {
		return r.save(v, c)
	}
	return nil
}

func sceneOwnershipFixture(t *testing.T) (*Server, *sceneTestRepository, *atomic.Int64) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	active := &atomic.Int64{}
	active.Store(1)
	repo := &sceneTestRepository{validate: func(v domain.CharacterSession) error {
		if v.Generation != active.Load() {
			return domain.ErrStaleOwner
		}
		return nil
	}}
	s := &Server{repo: repo, online: map[int64]domain.CharacterSession{}, maps: newMapSpace(800), drops: newDropSpace(), gameplay: g, skills: newSkillSpace(), lease: domain.WorldLease{WorldID: 1, InstanceID: "scene-test", Epoch: 7}}
	return s, repo, active
}
func sceneActor(s *Server, generation int64, mapID int) *liveCharacter {
	return &liveCharacter{session: domain.CharacterSession{AccountID: 2, CharacterID: 1, SessionID: "scene-owner", Generation: generation, ChannelID: 1, Lease: s.lease, Character: domain.Character{ID: 1, AccountID: 2, WorldID: 1, Name: "Scene", MapID: mapID, Level: 2, HP: 50, MP: 5, MaxHP: 50, MaxMP: 5, BaseMaxHP: 50, BaseMaxMP: 5, VitalsModel: 1, Revision: 1, STR: 12, DEX: 5, INT: 4, LUK: 4}}}
}
func sceneAssert(t *testing.T, s *Server, live *liveCharacter, sink packetSink) {
	t.Helper()
	v := s.sceneFor(live, sink)
	if v == nil || !s.sceneCurrent(v) {
		t.Fatal("current visit lost")
	}
	key := mapKey{v.owner.ChannelID, v.owner.Character.MapID}
	s.maps.mu.Lock()
	member := s.maps.rooms[key][v.owner.CharacterID]
	ok := member != nil && sameDropSink(member.sink, sink)
	s.maps.mu.Unlock()
	if !ok {
		t.Fatal("map owner/sink wrong")
	}
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[key]
	for k, other := range s.gameplay.rooms {
		if k != key && other.members[v.owner.CharacterID] != nil {
			t.Error("old map retained gameplay member")
		}
		for _, mob := range other.mobs {
			if mob.controller == v.owner.CharacterID && other.members[v.owner.CharacterID] == nil {
				t.Error("ghost mob controller")
			}
		}
	}
	gm := room.members[v.owner.CharacterID]
	ok = gm != nil && gm.live == live && sameDropSink(gm.sink, sink)
	s.gameplay.mu.Unlock()
	if !ok {
		t.Fatal("gameplay member wrong")
	}
	s.skills.mu.Lock()
	skill := s.skills.members[v.owner.CharacterID]
	ok = skill != nil && skill.live == live && sameDropSink(skill.sink, sink)
	s.skills.mu.Unlock()
	if !ok {
		t.Fatal("skills member wrong")
	}
	s.drops.mu.Lock()
	drop := s.drops.sinks[key][v.owner.CharacterID]
	s.drops.mu.Unlock()
	if !sameDropSink(drop, sink) {
		t.Fatal("drop sink wrong")
	}
}
func sceneAbsent(t *testing.T, s *Server) {
	t.Helper()
	s.mu.RLock()
	_, owner := s.online[1]
	_, visit := s.scenes[1]
	s.mu.RUnlock()
	if owner || visit {
		t.Fatal("removed scene resurrected online")
	}
	s.maps.mu.Lock()
	for _, room := range s.maps.rooms {
		if room[1] != nil {
			t.Error("map ghost")
		}
	}
	s.maps.mu.Unlock()
	s.gameplay.mu.Lock()
	for _, room := range s.gameplay.rooms {
		if room.members[1] != nil {
			t.Error("gameplay ghost")
		}
		for _, mob := range room.mobs {
			if mob.controller == 1 {
				t.Error("ghost controller")
			}
		}
	}
	s.gameplay.mu.Unlock()
	s.skills.mu.Lock()
	if s.skills.members[1] != nil {
		t.Error("skill ghost")
	}
	s.skills.mu.Unlock()
	s.drops.mu.Lock()
	for _, room := range s.drops.sinks {
		if room[1] != nil {
			t.Error("drop ghost")
		}
	}
	s.drops.mu.Unlock()
}
func packetCount(s *gameplayTestSink) int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.packets) }

func TestSceneInitialClaimRequiresDurableValidator(t *testing.T) {
	s, _, _ := sceneOwnershipFixture(t)
	s.repo = struct{ domain.Repository }{}
	live, sink := sceneActor(s, 1, 10000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), live, sink, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("missing validator trusted", err)
	}
	if packetCount(sink) != 0 || live.visit != nil {
		t.Fatal("rejected claim published")
	}
	sceneAbsent(t, s)
}

func TestSceneOldCommittedWarpCannotOutliveReplacement(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "new_owner_stays", true: "new_owner_disconnects"}[disconnect], func(t *testing.T) {
			s, repo, active := sceneOwnershipFixture(t)
			old, oldSink := sceneActor(s, 1, 10000), &gameplayTestSink{}
			if err := s.publishInitialScene(context.Background(), old, oldSink, 1); err != nil {
				t.Fatal(err)
			}
			peer, peerSink := sceneActor(s, 1, 10000), &gameplayTestSink{}
			peer.session.CharacterID = 2
			peer.session.Character.ID = 2
			peer.session.AccountID = 3
			peer.session.Character.AccountID = 3
			if err := s.publishInitialScene(context.Background(), peer, peerSink, 1); err != nil {
				t.Fatal(err)
			}
			committed, release := make(chan struct{}), make(chan struct{})
			repo.save = func(domain.CharacterSession, domain.Character) error { close(committed); <-release; return nil }
			destination, err := s.gameplay.catalog.Map(20000)
			if err != nil {
				t.Fatal(err)
			}
			portal, _ := destination.Spawn()
			done := make(chan error, 1)
			go func() { done <- s.warpGameplay(context.Background(), 1, old, oldSink, 20000, portal) }()
			select {
			case <-committed:
			case <-time.After(time.Second):
				t.Fatal("warp did not commit")
			}
			active.Store(2)
			replacement, replacementSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
			if err = s.publishInitialScene(context.Background(), replacement, replacementSink, 1); err != nil {
				t.Fatal(err)
			}
			if disconnect {
				s.leaveScene(replacement, replacementSink)
				active.Store(3)
			} else {
				sceneAssert(t, s, replacement, replacementSink)
			}
			beforeOld, beforeNew, beforePeer := packetCount(oldSink), packetCount(replacementSink), packetCount(peerSink)
			close(release)
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("old warp stuck")
			}
			if !errors.Is(err, domain.ErrStaleOwner) {
				t.Fatal("stale commit published", err)
			}
			if old.session.Character.MapID != 10000 || packetCount(oldSink) != beforeOld || packetCount(replacementSink) != beforeNew || packetCount(peerSink) != beforePeer {
				t.Fatal("old commit mutated live or emitted packets")
			}
			if disconnect {
				sceneAbsent(t, s)
			} else {
				sceneAssert(t, s, replacement, replacementSink)
			}
		})
	}
}

func TestSceneDelayedInitialClaimAfterReplacementDisconnected(t *testing.T) {
	s, _, active := sceneOwnershipFixture(t)
	active.Store(2)
	newLive, newSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), newLive, newSink, 1); err != nil {
		t.Fatal(err)
	}
	s.leaveScene(newLive, newSink)
	active.Store(3)
	old, oldSink := sceneActor(s, 1, 10000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), old, oldSink, 1); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("empty online trusted delayed claim", err)
	}
	if packetCount(oldSink) != 0 {
		t.Fatal("delayed claim sent field")
	}
	sceneAbsent(t, s)
}

type sceneBlockingSink struct {
	gameplayTestSink
	once             sync.Once
	entered, release chan struct{}
}

func (s *sceneBlockingSink) WritePacket(p []byte) error {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.gameplayTestSink.WritePacket(p)
}

func TestSceneValidatedClaimPublishesBeforeNewerClaimWaitingAtGate(t *testing.T) {
	s, repo, active := sceneOwnershipFixture(t)
	old := sceneActor(s, 1, 10000)
	blocked := &sceneBlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	oldDone := make(chan error, 1)
	go func() { oldDone <- s.publishInitialScene(context.Background(), old, blocked, 1) }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("initial field packet not reached")
	}
	// Durable replacement commits while the old, already-validated publisher
	// owns the gate. It must not validate/install locally until that gate exits.
	active.Store(2)
	newValidated := make(chan struct{}, 1)
	repo.validate = func(v domain.CharacterSession) error {
		if v.Generation == 2 {
			newValidated <- struct{}{}
		}
		if v.Generation != active.Load() {
			return domain.ErrStaleOwner
		}
		return nil
	}
	newLive, newSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
	newDone := make(chan error, 1)
	go func() { newDone <- s.publishInitialScene(context.Background(), newLive, newSink, 1) }()
	select {
	case <-newValidated:
		t.Fatal("new local claim interleaved field publication")
	case <-time.After(30 * time.Millisecond):
	}
	if packetCount(newSink) != 0 {
		t.Fatal("new sink received interleaved publication")
	}
	close(blocked.release)
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	if err := <-newDone; err != nil {
		t.Fatal(err)
	}
	sceneAssert(t, s, newLive, newSink)
}

func TestSceneSameMapReentryRejectsOldVisitPublication(t *testing.T) {
	s, _, _ := sceneOwnershipFixture(t)
	live, sink := sceneActor(s, 1, 10000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), live, sink, 1); err != nil {
		t.Fatal(err)
	}
	oldVisit := s.sceneFor(live, sink)
	before := live.session
	m, _ := s.gameplay.catalog.Map(10000)
	entry, _ := m.Spawn()
	if err := s.warpGameplay(context.Background(), 1, live, sink, 10000, entry); err != nil {
		t.Fatal(err)
	}
	if live.visit == oldVisit {
		t.Fatal("same-map warp reused visit token")
	}
	count := packetCount(sink)
	stale := before
	stale.Character.Revision++
	live.persistMu.Lock()
	err := s.publishGameplayWarp(context.Background(), 1, live, sink, before.Character, stale, false, oldVisit, nil, false)
	live.persistMu.Unlock()
	if !errors.Is(err, domain.ErrStaleOwner) || packetCount(sink) != count {
		t.Fatal("old same-map visit published", err)
	}
	sceneAssert(t, s, live, sink)
}

// The CID gate must cover the gap after expected-owner validation and before
// map.Enter: pausing the actual WarpMap write makes that gap deterministic.
func TestSceneCommittedWarpCannotInterleaveReplacementBeforeMapEnter(t *testing.T) {
	s, _, active := sceneOwnershipFixture(t)
	old := sceneActor(s, 1, 10000)
	sink := &gatedWarpSink{entered: make(chan struct{}), release: make(chan struct{})}
	// Initial SetField uses the same opcode. Install without blocking first.
	close(sink.release)
	if err := s.publishInitialScene(context.Background(), old, sink, 1); err != nil {
		t.Fatal(err)
	}
	sink.once = sync.Once{}
	sink.entered = make(chan struct{})
	sink.release = make(chan struct{})
	destination, _ := s.gameplay.catalog.Map(20000)
	entry, _ := destination.Spawn()
	done := make(chan error, 1)
	go func() { done <- s.warpGameplay(context.Background(), 1, old, sink, 20000, entry) }()
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("warp publication did not pause")
	}
	active.Store(2)
	replacement, newSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
	claimed := make(chan error, 1)
	go func() { claimed <- s.publishInitialScene(context.Background(), replacement, newSink, 1) }()
	select {
	case err := <-claimed:
		t.Fatalf("replacement entered inside old publication: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(sink.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-claimed; err != nil {
		t.Fatal(err)
	}
	sceneAssert(t, s, replacement, newSink)
	count := packetCount(newSink)
	s.leaveScene(old, sink)
	if packetCount(newSink) != count {
		t.Fatal("late old cleanup wrote replacement")
	}
	sceneAssert(t, s, replacement, newSink)
}

func TestSceneStaleTransitionalAcknowledgementIsSuppressed(t *testing.T) {
	s, _, active := sceneOwnershipFixture(t)
	old, sink := sceneActor(s, 1, 10000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), old, sink, 1); err != nil {
		t.Fatal(err)
	}
	expected := s.sceneFor(old, sink)
	active.Store(2)
	replacement, newSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), replacement, newSink, 1); err != nil {
		t.Fatal(err)
	}
	count := packetCount(sink)
	if err := s.writeScenePackets(expected, protocol.EnableActions()); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal(err)
	}
	if packetCount(sink) != count {
		t.Fatal("stale acknowledgement emitted")
	}
	sceneAssert(t, s, replacement, newSink)
}

func TestSceneCommittedScriptTravelCannotPublishIntoReplacement(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "replacement_left"}[disconnect], func(t *testing.T) {
			s, _, active := sceneOwnershipFixture(t)
			old, sink := sceneActor(s, 1, 10000), &gameplayTestSink{}
			if err := s.publishInitialScene(context.Background(), old, sink, 1); err != nil {
				t.Fatal(err)
			}
			before := cloneCombatSession(old.session)
			expected := s.sceneFor(old, sink)
			result := domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: before.Character}}
			result.Character.MapID = 20000
			result.Character.Mesos = 123
			result.Character.Revision++
			active.Store(2)
			replacement, nextSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
			if err := s.publishInitialScene(context.Background(), replacement, nextSink, 1); err != nil {
				t.Fatal(err)
			}
			if disconnect {
				s.leaveScene(replacement, nextSink)
			}
			oldCount, newCount := packetCount(sink), packetCount(nextSink)
			a := &worldScript{server: s, live: old, sink: sink}
			old.persistMu.Lock()
			_, err := a.publishScriptTravel(context.Background(), before, result, expected)
			old.persistMu.Unlock()
			if !errors.Is(err, domain.ErrStaleOwner) {
				t.Fatal("committed old fare published", err)
			}
			if old.session.Character.Mesos != before.Character.Mesos || old.session.Character.MapID != before.Character.MapID || packetCount(sink) != oldCount || packetCount(nextSink) != newCount {
				t.Fatal("stale fare mutated aggregate or emitted inventory/mesos/field")
			}
			if disconnect {
				sceneAbsent(t, s)
			} else {
				sceneAssert(t, s, replacement, nextSink)
			}
		})
	}
}

type sceneCancellationRepository struct {
	*sceneTestRepository
	domain.SkillRepository
	tick func(domain.CharacterSession) (domain.SkillResult, error)
}

func (r *sceneCancellationRepository) TickSkillBuffs(_ context.Context, v domain.CharacterSession) (domain.SkillResult, error) {
	return r.tick(v)
}
func TestSceneCommittedDeathCancellationCannotMutateReplacementVisit(t *testing.T) {
	s, base, active := sceneOwnershipFixture(t)
	old, sink := sceneActor(s, 1, 40000), &gameplayTestSink{}
	old.session.Character.HP = 0
	old.session.Character.SkillBuffs = []domain.SkillBuff{{ID: 1002, Stat: "SPEED", Level: 1, Value: 10, ExpiresAt: time.Now().Add(time.Minute)}}
	if err := s.publishInitialScene(context.Background(), old, sink, 1); err != nil {
		t.Fatal(err)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	var saves atomic.Int32
	base.save = func(domain.CharacterSession, domain.Character) error { saves.Add(1); return nil }
	s.repo = &sceneCancellationRepository{sceneTestRepository: base, tick: func(v domain.CharacterSession) (domain.SkillResult, error) {
		c := v.Character
		c.SkillBuffs = nil
		c.Revision++
		close(committed)
		<-release
		return domain.SkillResult{Character: c, Changed: true, Expired: v.Character.SkillBuffs}, nil
	}}
	destination, _ := s.gameplay.catalog.Map(10000)
	entry, _ := destination.Spawn()
	done := make(chan error, 1)
	go func() {
		done <- s.warpGameplayTransition(context.Background(), 1, old, sink, 10000, entry, &revivalTransition{fromMap: 40000})
	}()
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not commit")
	}
	active.Store(2)
	replacement, nextSink := sceneActor(s, 2, 40000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), replacement, nextSink, 1); err != nil {
		t.Fatal(err)
	}
	beforeOld, beforeNew := packetCount(sink), packetCount(nextSink)
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrStaleOwner) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stuck")
	}
	if saves.Load() != 0 || len(old.session.Character.SkillBuffs) != 1 || old.session.Character.Revision != 1 || packetCount(sink) != beforeOld || packetCount(nextSink) != beforeNew {
		t.Fatal("old cancellation merged/published before visit validation")
	}
	sceneAssert(t, s, replacement, nextSink)
}

func TestSceneSameMapReentryRejectsOldMovementAndKeepsCurrentMovementSQLFree(t *testing.T) {
	s, repo, _ := sceneOwnershipFixture(t)
	live, sink := sceneActor(s, 1, 10000), &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), live, sink, 1); err != nil {
		t.Fatal(err)
	}
	old := s.sceneFor(live, sink)
	destination, _ := s.gameplay.catalog.Map(10000)
	entry, _ := destination.Spawn()
	if err := s.warpGameplay(context.Background(), 1, live, sink, 10000, entry); err != nil {
		t.Fatal(err)
	}
	// A local callback which pinned the old visit before waiting for the gate
	// must not move a same-live/same-sink/same-map replacement visit.
	packet := protocol.EncodeAbsolutePlayerMove(0, 0, 123, 45)
	before := live.session.Character
	count := packetCount(sink)
	if err := s.moveSceneVisit(old, protocol.NewReader(packet[2:])); err != nil {
		t.Fatal(err)
	}
	if live.session.Character.X != before.X || live.session.Character.Y != before.Y || packetCount(sink) != count {
		t.Fatal("old movement mutated reentry")
	}
	repo.validate = func(domain.CharacterSession) error {
		t.Fatal("movement queried durable authority")
		return domain.ErrForbidden
	}
	repo.save = func(domain.CharacterSession, domain.Character) error {
		t.Fatal("movement persisted synchronously")
		return domain.ErrForbidden
	}
	if err := s.moveScene(live, sink, protocol.NewReader(packet[2:])); err != nil {
		t.Fatal(err)
	}
	if live.session.Character.X != 123 || live.session.Character.Y != 45 || !live.dirty {
		t.Fatal("current movement rejected")
	}
	sceneAssert(t, s, live, sink)
}
