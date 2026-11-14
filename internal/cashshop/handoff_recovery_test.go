package cashshop

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

type recoveryRepo struct {
	domain.CashRepository
	owned    []domain.OwnedSession
	released []domain.CashSession
}

func (r *recoveryRepo) ListCashSessions(context.Context, domain.ServiceLease) ([]domain.OwnedSession, error) {
	return r.owned, nil
}

func (r *recoveryRepo) ReleaseCashCharacter(_ context.Context, c domain.CashSession) error {
	r.released = append(r.released, c)
	return nil
}

func TestCashOrphanReleasedAfterGraceOnly(t *testing.T) {
	live := domain.CashSession{Session: domain.CharacterSession{AccountID: 1, CharacterID: 10, SessionID: "live", Generation: 3}}
	repo := &recoveryRepo{owned: []domain.OwnedSession{
		{AccountID: 1, CharacterID: 10, SessionID: "live", Generation: 3},
		{AccountID: 2, CharacterID: 20, SessionID: "lost", Generation: 6},
	}}
	s := &server{repo: repo, lease: domain.ServiceLease{Service: "cashshop", InstanceID: "c", Epoch: 4}}
	s.trackLive(live)
	start := time.Now()
	s.reclaimOrphanSessions(context.Background(), start)
	s.reclaimOrphanSessions(context.Background(), start.Add(orphanSessionGrace-time.Second))
	if len(repo.released) != 0 {
		t.Fatalf("released inside grace: %+v", repo.released)
	}
	s.reclaimOrphanSessions(context.Background(), start.Add(orphanSessionGrace))
	if len(repo.released) != 1 {
		t.Fatalf("released %+v", repo.released)
	}
	got := repo.released[0]
	if got.Session.AccountID != 2 || got.Session.CharacterID != 20 || got.Session.Generation != 6 || got.Lease.Epoch != 4 {
		t.Fatalf("released wrong session %+v", got)
	}
}

func TestCashLiveTrackingSurvivesDuplicateSockets(t *testing.T) {
	c := domain.CashSession{Session: domain.CharacterSession{AccountID: 1, SessionID: "s", Generation: 2}}
	s := &server{}
	s.trackLive(c)
	s.trackLive(c)
	s.untrackLive(c)
	if s.live[keyOf(c)] != 1 {
		t.Fatal("one socket close dropped the other's claim")
	}
	s.untrackLive(c)
	if len(s.live) != 0 {
		t.Fatal("live claim leaked")
	}
}
