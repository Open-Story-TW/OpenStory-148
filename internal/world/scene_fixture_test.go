package world

import (
	"context"
	"testing"

	"maple-go-server/internal/domain"
)

// Existing business-policy tests construct an already authenticated aggregate
// directly, without exercising handoff SQL. Make that fixture authority explicit
// and install its visit, rather than weakening the production missing-validator
// rejection. Scene authority/claim tests use SceneRepository fakes or real PG.
type establishedSceneFixtureAuthority struct{}

func (establishedSceneFixtureAuthority) ValidateSceneOwner(_ context.Context, v domain.CharacterSession) error {
	if v.CharacterID <= 0 || v.Character.ID != v.CharacterID {
		return domain.ErrForbidden
	}
	return nil
}
func seedBoundScene(t *testing.T, s *Server, live *liveCharacter, sink packetSink) {
	t.Helper()
	live.mu.Lock()
	if live.session.ChannelID == 0 {
		live.session.ChannelID = 1
	}
	snapshot := live.session
	live.mu.Unlock()
	if err := (establishedSceneFixtureAuthority{}).ValidateSceneOwner(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lockScenePublication(snapshot.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	s.installScene(makeSceneVisit(snapshot, live, sink), snapshot)
}
