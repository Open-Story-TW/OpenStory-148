package world

import (
	"maple-go-server/internal/domain"
	"testing"
)

func TestLateTransferCleanupCannotRemoveAnyReplacementRegistry(t *testing.T) {
	old := partyRuntime(11, "Alice", 1, 10000)
	old.session.Generation = 2
	next := partyRuntime(11, "Alice", 1, 10000)
	next.session.Generation = 4
	next.session.SessionID = "reconnected-after-transfer-expired"
	first, second := &memSink{}, &memSink{}
	s := &Server{maps: newMapSpace(800), drops: newDropSpace(), skills: newSkillSpace(), online: map[int64]domain.CharacterSession{}}
	s.maps.Enter(1, 10000, old.session.Character, first)
	s.drops.Track(1, 10000, 11, first, old.session)
	s.skills.members[11] = &skillMember{live: old, sink: first}
	s.online[11] = old.session
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		<-release
		s.maps.Leave(1, 10000, 11, first)
		s.leaveSkills(11, old)
		s.drops.Untrack(1, 10000, 11, first)
		s.removeOnlineSession(old.session)
		close(done)
	}()
	<-started
	// A new valid generation registers while an expired transfer's source is
	// still blocked in an earlier cleanup step. No global check can guard all
	// following deletes, so every registry must compare under its own mutex.
	s.maps.Enter(1, 10000, next.session.Character, second)
	s.drops.Track(1, 10000, 11, second, next.session)
	s.skills.members[11] = &skillMember{live: next, sink: second}
	s.online[11] = next.session
	close(release)
	<-done
	if !sameDropSink(s.maps.rooms[mapKey{1, 10000}][11].sink, second) || s.skills.members[11].live != next || !sameDropSink(s.drops.sinks[mapKey{1, 10000}][11], second) || s.online[11].Generation != 4 {
		t.Fatal("old cleanup erased replacement presence")
	}
	s.maps.Leave(1, 10000, 11, second)
	s.leaveSkills(11, next)
	s.drops.Untrack(1, 10000, 11, second)
	s.removeOnlineSession(next.session)
	if len(s.maps.rooms) != 0 || len(s.skills.members) != 0 || len(s.drops.sinks) != 0 || len(s.drops.questTracks) != 0 || len(s.online) != 0 {
		t.Fatal("current connection cleanup did not remove its own registries")
	}
	// Repeated old cleanup cannot remove another owner's data or panic.
	s.maps.Leave(1, 10000, 11, first)
	s.leaveSkills(11, old)
	s.drops.Untrack(1, 10000, 11, first)
	s.removeOnlineSession(old.session)
}

func TestOnlineRemovalRequiresMatchingSessionGenerationAndLease(t *testing.T) {
	current := domain.CharacterSession{AccountID: 1, CharacterID: 2, SessionID: "new", Generation: 4, Lease: domain.WorldLease{InstanceID: "world-current", Epoch: 2}}
	for _, change := range []func(*domain.CharacterSession){func(s *domain.CharacterSession) { s.Generation-- }, func(s *domain.CharacterSession) { s.SessionID = "old" }, func(s *domain.CharacterSession) { s.Lease.Epoch-- }, func(s *domain.CharacterSession) { s.Lease.InstanceID = "old-world" }} {
		s := &Server{online: map[int64]domain.CharacterSession{2: current}}
		old := current
		change(&old)
		s.removeOnlineSession(old)
		if len(s.online) != 1 {
			t.Fatal("stale session removed current online entry")
		}
	}
}
