package world

import (
	"context"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

type handoffRepo struct {
	domain.Repository
	finishErrs []error
	committed  bool
	owned      []domain.OwnedSession
	released   []domain.CharacterSession
	saved      []domain.CharacterSession
}

func (r *handoffRepo) FinishChannelTransfer(_ context.Context, session domain.CharacterSession, target int) (domain.CharacterSession, error) {
	if r.committed {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	// Each scripted error is a reply lost after the commit landed.
	r.committed = true
	if len(r.finishErrs) > 0 {
		err := r.finishErrs[0]
		r.finishErrs = r.finishErrs[1:]
		return domain.CharacterSession{}, err
	}
	session.Generation++
	session.ChannelID = target
	return session, nil
}

func (r *handoffRepo) ResumeChannelTransfer(_ context.Context, session domain.CharacterSession, target int) (domain.CharacterSession, error) {
	if !r.committed {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	session.Generation++
	session.ChannelID = target
	return session, nil
}

func (r *handoffRepo) ListWorldSessions(context.Context, domain.WorldLease) ([]domain.OwnedSession, error) {
	return r.owned, nil
}

func (r *handoffRepo) ReleaseCharacter(_ context.Context, session domain.CharacterSession) error {
	r.released = append(r.released, session)
	return nil
}

func (r *handoffRepo) SaveCharacter(_ context.Context, session domain.CharacterSession, _ domain.Character) error {
	r.saved = append(r.saved, session)
	return nil
}

func pendingTransferServer(repo *handoffRepo, dirty bool) *Server {
	session := domain.CharacterSession{AccountID: 7, CharacterID: 70, SessionID: "s", Generation: 4, ChannelID: 1, Character: domain.Character{ID: 70, X: 321}}
	return &Server{repo: repo, online: map[int64]domain.CharacterSession{}, channelTransfers: map[channelTransferKey]*channelTransfer{
		{handle: 99, channel: 2}: {session: session, nativeHandle: 99, dirty: dirty, ip: "127.0.0.1", expires: time.Now().Add(30 * time.Second)},
	}}
}

func TestChannelResendResumesLostCommit(t *testing.T) {
	repo := &handoffRepo{finishErrs: []error{context.DeadlineExceeded}}
	s := pendingTransferServer(repo, true)
	if _, matched, err := s.consumeChannel(context.Background(), 99, 2, "127.0.0.1"); !matched || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first claim matched=%v err=%v", matched, err)
	}
	if pending := s.channelTransfers[channelTransferKey{99, 2}]; pending == nil || !pending.uncertain || pending.claiming {
		t.Fatalf("lost reply did not keep an uncertain pending transfer: %+v", pending)
	}
	live, matched, err := s.consumeChannel(context.Background(), 99, 2, "127.0.0.1")
	if err != nil || !matched {
		t.Fatalf("resend matched=%v err=%v", matched, err)
	}
	if live.session.Generation != 5 || live.session.ChannelID != 2 || live.session.Character.X != 321 || !live.dirty || live.nativeHandle != 99 {
		t.Fatalf("resumed live=%+v", live.session)
	}
	if len(s.channelTransfers) != 0 {
		t.Fatal("resumed transfer still pending")
	}
	if _, matched, _ = s.consumeChannel(context.Background(), 99, 2, "127.0.0.1"); matched {
		t.Fatal("third arrival matched a consumed transfer")
	}
}

func TestChannelRejectionIsNotTreatedAsUncertain(t *testing.T) {
	repo := &handoffRepo{finishErrs: []error{domain.ErrStaleOwner}}
	s := pendingTransferServer(repo, false)
	if _, _, err := s.consumeChannel(context.Background(), 99, 2, "127.0.0.1"); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal(err)
	}
	if s.channelTransfers[channelTransferKey{99, 2}].uncertain {
		t.Fatal("definite rejection marked uncertain")
	}
}

func TestExpiredUncertainTransferReleasesCommittedGeneration(t *testing.T) {
	repo := &handoffRepo{committed: true}
	s := pendingTransferServer(repo, true)
	pending := s.channelTransfers[channelTransferKey{99, 2}]
	pending.uncertain = true
	pending.expires = time.Now().Add(-time.Second)
	s.expireChannelTransfers(context.Background())
	if len(repo.saved) != 1 || repo.saved[0].Generation != 5 {
		t.Fatalf("dirty progress not saved under committed generation: %+v", repo.saved)
	}
	if len(repo.released) != 1 || repo.released[0].Generation != 5 || repo.released[0].ChannelID != 2 {
		t.Fatalf("released %+v", repo.released)
	}
	if len(s.channelTransfers) != 0 {
		t.Fatal("expired transfer kept")
	}
}

func TestExpiredTransferReleasesFrozenGeneration(t *testing.T) {
	repo := &handoffRepo{}
	s := pendingTransferServer(repo, false)
	s.channelTransfers[channelTransferKey{99, 2}].expires = time.Now().Add(-time.Second)
	s.expireChannelTransfers(context.Background())
	if len(repo.saved) != 0 || len(repo.released) != 1 || repo.released[0].Generation != 4 {
		t.Fatalf("saved=%+v released=%+v", repo.saved, repo.released)
	}
}

func TestOrphanSessionsReleasedOnlyAfterGrace(t *testing.T) {
	live := domain.CharacterSession{AccountID: 1, CharacterID: 10, SessionID: "live", Generation: 3}
	repo := &handoffRepo{owned: []domain.OwnedSession{
		{AccountID: 1, CharacterID: 10, SessionID: "live", Generation: 3, ChannelID: 1},
		{AccountID: 2, CharacterID: 20, SessionID: "lost", Generation: 8, ChannelID: 2},
		{AccountID: 3, CharacterID: 30, SessionID: "moving", Generation: 4, ChannelID: 1},
	}}
	s := &Server{repo: repo, lease: domain.WorldLease{WorldID: 1, InstanceID: "w", Epoch: 9}, online: map[int64]domain.CharacterSession{10: live}, channelTransfers: map[channelTransferKey]*channelTransfer{
		{handle: 5, channel: 2}: {session: domain.CharacterSession{AccountID: 3, CharacterID: 30, SessionID: "moving", Generation: 4}},
	}}
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
	if got.AccountID != 2 || got.CharacterID != 20 || got.SessionID != "lost" || got.Generation != 8 || got.Lease.Epoch != 9 {
		t.Fatalf("released wrong session %+v", got)
	}
}

func TestOrphanTimerResetsWhenSessionBecomesLive(t *testing.T) {
	repo := &handoffRepo{owned: []domain.OwnedSession{{AccountID: 2, CharacterID: 20, SessionID: "slow", Generation: 8}}}
	s := &Server{repo: repo, online: map[int64]domain.CharacterSession{}}
	start := time.Now()
	s.reclaimOrphanSessions(context.Background(), start)
	// The claiming socket installs its scene before the grace runs out.
	s.online[20] = domain.CharacterSession{AccountID: 2, CharacterID: 20, SessionID: "slow", Generation: 8}
	s.reclaimOrphanSessions(context.Background(), start.Add(time.Second))
	delete(s.online, 20)
	s.reclaimOrphanSessions(context.Background(), start.Add(orphanSessionGrace))
	if len(repo.released) != 0 {
		t.Fatalf("grace not restarted: %+v", repo.released)
	}
	// A newer generation is a different claim and never matches the old one.
	s.online[20] = domain.CharacterSession{AccountID: 2, CharacterID: 20, SessionID: "slow", Generation: 9}
	s.reclaimOrphanSessions(context.Background(), start.Add(2*orphanSessionGrace))
	if len(repo.released) != 1 || repo.released[0].Generation != 8 {
		t.Fatalf("stale generation not released: %+v", repo.released)
	}
}
