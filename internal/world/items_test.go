package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

type itemTestRepository struct {
	domain.Repository
	use  func(domain.UseItemRequest) (domain.Character, error)
	save func(domain.CharacterSession, domain.Character) error
}

func (r itemTestRepository) UseItem(_ context.Context, req domain.UseItemRequest) (domain.Character, error) {
	return r.use(req)
}

func (r itemTestRepository) SaveCharacter(_ context.Context, session domain.CharacterSession, value domain.Character) error {
	return r.save(session, value)
}

func TestItemUseSerializesWithCheckpointAndPreservesMovement(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	saved := make(chan domain.Character, 1)
	repo := itemTestRepository{
		use: func(req domain.UseItemRequest) (domain.Character, error) {
			close(entered)
			<-release
			value := req.Session.Character
			value.HP = 55
			value.Revision++
			value.Items = []domain.InventoryItem{{TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 4}}
			return value, nil
		},
		save: func(_ domain.CharacterSession, value domain.Character) error {
			saved <- value
			return nil
		},
	}
	s := &Server{repo: repo, online: make(map[int64]domain.CharacterSession)}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, HP: 5, MaxHP: 100, Revision: 7}}}
	s.online[1] = live.session // The authenticated claim owns the initial projection.
	useDone := make(chan error, 1)
	go func() {
		_, err := s.useItem(context.Background(), live, domain.UseItemRequest{Slot: 1, TemplateID: 2000000})
		useDone <- err
	}()
	<-entered
	live.mu.Lock()
	live.session.Character.X = 80
	live.session.Character.Y = 20
	live.dirty = true
	live.mu.Unlock()
	checkpointDone := make(chan error, 1)
	go func() { checkpointDone <- s.checkpoint(context.Background(), live) }()
	select {
	case <-saved:
		close(release)
		t.Fatal("checkpoint entered repository while item transaction was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan error{useDone, checkpointDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("durable operation did not finish")
		}
	}
	value := <-saved
	if value.HP != 55 || value.Revision != 8 || value.X != 80 || value.Y != 20 || value.Items[0].Quantity != 4 {
		t.Fatalf("checkpoint received stale item/progress state: %+v", value)
	}
	if live.session.Character.Revision != 9 || live.dirty || s.online[1].Character.HP != 55 {
		t.Fatalf("wrong live state after checkpoint: %+v", live)
	}
}

func TestLateItemCommitCannotOverwriteReplacementLogin(t *testing.T) {
	original := domain.CharacterSession{AccountID: 1, CharacterID: 2, SessionID: "old-session", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 2, HP: 5, MaxHP: 100, Revision: 7}}
	replacement := original
	replacement.SessionID, replacement.Generation = "replacement", 2
	replacement.Character.MapID, replacement.Character.HP = 10000, 99
	s := &Server{online: map[int64]domain.CharacterSession{2: original}}
	s.repo = itemTestRepository{use: func(req domain.UseItemRequest) (domain.Character, error) {
		// The old transaction committed before a new login claimed local state,
		// but its response reaches this handler only after that replacement.
		s.mu.Lock()
		s.online[2] = replacement
		s.mu.Unlock()
		updated := req.Session.Character
		updated.HP, updated.Revision = 55, 8
		return updated, nil
	}}
	if _, err := s.useItem(context.Background(), &liveCharacter{session: original}, domain.UseItemRequest{Slot: 1, TemplateID: 2000000}); err != nil {
		t.Fatal(err)
	}
	got := s.online[2]
	if !sameSceneOwner(got, replacement) || got.Character.MapID != 10000 || got.Character.HP != 99 {
		t.Fatalf("late item result overwrote replacement login: %+v", got)
	}
}
