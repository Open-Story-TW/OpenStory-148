package world

import (
	"runtime"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestOnlineUpdateCannotInstallOrOverwriteAnotherOwner(t *testing.T) {
	original := domain.CharacterSession{CharacterID: 3, AccountID: 4, SessionID: "owner", Generation: 5, ChannelID: 1, Lease: domain.WorldLease{WorldID: 2, InstanceID: "world", Epoch: 6}}
	for _, name := range []string{"absent", "account", "character", "session", "generation", "channel", "world", "instance", "epoch"} {
		t.Run(name, func(t *testing.T) {
			current := original
			switch name {
			case "account":
				current.AccountID++
			case "character":
				current.CharacterID++
			case "session":
				current.SessionID += "-new"
			case "generation":
				current.Generation++
			case "channel":
				current.ChannelID++
			case "world":
				current.Lease.WorldID++
			case "instance":
				current.Lease.InstanceID += "-new"
			case "epoch":
				current.Lease.Epoch++
			}
			s := &Server{online: map[int64]domain.CharacterSession{original.CharacterID: current}}
			if name == "absent" {
				delete(s.online, original.CharacterID)
			}
			next := original
			next.Character.MapID = 10000
			if s.updateOnlineOwned(original, next) {
				t.Fatal("stale owner updated online state")
			}
			if name != "absent" && !sameSceneOwner(s.online[original.CharacterID], current) {
				t.Fatal("replacement owner changed")
			}
		})
	}
	s := &Server{online: map[int64]domain.CharacterSession{original.CharacterID: original}}
	next := original
	next.Character.MapID, next.Character.Revision = 10000, 42
	next.Lease.ExpiresAt = time.Now().Add(time.Minute)
	if !s.updateOnlineOwned(original, next) || s.online[3].Character.Revision != 42 {
		t.Fatal("current owner's mutable projection did not update")
	}
	wrong := next
	wrong.Generation++
	if s.updateOnlineOwned(next, wrong) {
		t.Fatal("CAS smuggled a new generation")
	}
}

func TestSceneGatesSerializeOneCharacterAndReleaseRegistry(t *testing.T) {
	s := &Server{}
	first, err := s.lockScenePublication(3)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		unlock, e := s.lockScenePublication(3)
		if e != nil {
			panic(e)
		}
		close(entered)
		unlock()
		close(finished)
	}()
	other, err := s.lockScenePublication(4)
	if err != nil {
		t.Fatal(err)
	}
	other()
	deadline := time.Now().Add(time.Second)
	for {
		s.sceneMu.Lock()
		waiting := s.sceneGates[3] != nil && s.sceneGates[3].refs == 2
		s.sceneMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			first()
			t.Fatal("same-character waiter never registered")
		}
		runtime.Gosched()
	}
	select {
	case <-entered:
		t.Fatal("same character overlapped")
	default:
	}
	first()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("waiter did not progress")
	}
	s.sceneMu.Lock()
	remaining := len(s.sceneGates)
	s.sceneMu.Unlock()
	if remaining != 0 {
		t.Fatal("idle scene gates retained", remaining)
	}
	if _, err := s.lockScenePublication(0); err == nil {
		t.Fatal("invalid actor accepted")
	}
}
