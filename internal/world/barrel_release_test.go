package world

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The real release packet creates a barrel in front of the character. Its
// later collision attack owns on-hit rules. This test deliberately makes no
// stun claim: the current WZ time=900 becomes 900 seconds and exceeds the
// status resolver's ten-minute bound; native push and Java STUN are distinct.
func TestBarrelReleaseDoesNotStunCasterNeighbours(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	live := lives[0]
	live.session.Character.Job = 530
	live.session.Character.Level = 30
	live.session.Character.Skills = []domain.SkillState{{ID: 5301001, Level: 20}}
	seedBoundScene(t, s, live, sinks[0])
	mob.hp = 1000 // Survives collision: its resulting status can be inspected.
	mob.life.X, mob.life.Y = live.session.Character.X, live.session.Character.Y
	var requests []domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		requests = append(requests, q)
		return fakeSharedResult(q), nil
	}}
	w := protocol.NewWriter(protocol.RecvThrowGrenade)
	w.I32(int32(live.session.Character.X + 40))
	w.I32(int32(live.session.Character.Y))
	w.I32(int32(live.session.Character.Y))
	w.I32(800)
	w.I32(5301001)
	w.U8(0)
	w.I32(364)
	w.I32(1)
	s.throwGrenade(context.Background(), 1, live, sinks[0], w.Packet()[2:])
	if len(requests) != 1 || requests[0].Cost == nil || requests[0].Cost.MP != 28 || len(requests[0].Targets) != 0 {
		t.Fatalf("release must still settle its WZ cost once: %+v", requests)
	}
	if mob.statuses.Has(combat.MobStun) || sinks[1].count(protocol.SendApplyMonsterStatus) != 0 {
		t.Fatal("throw stunned a neighbour without a collision")
	}
	// Start a distinct collision frame after the ordinary input rate window.
	s.gameplay.mu.Lock()
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].members[live.session.CharacterID].attacked = false
	s.gameplay.mu.Unlock()
	a := genericMelee(5301001, 123, mob.objectID, 1, 1, 1)
	a.X, a.Y = int16(live.session.Character.X), int16(live.session.Character.Y)
	if err := s.genericAttackGameplay(context.Background(), 1, live, sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if mob.hp != 997 || len(requests) != 2 || len(requests[1].Targets) != 1 {
		t.Fatalf("actual collision must still apply damage: hp=%d requests=%+v", mob.hp, requests)
	}
}

func barrelTestRelease(live *liveCharacter, key int32) []byte {
	c := live.session.Character
	w := protocol.NewWriter(protocol.RecvThrowGrenade)
	w.I32(int32(c.X + 40))
	w.I32(int32(c.Y))
	w.I32(int32(c.Y))
	w.I32(800)
	w.I32(5301001)
	w.U8(0)
	w.I32(364)
	w.I32(key)
	return w.Packet()[2:]
}

func barrelTestFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	live := lives[0]
	live.session.Character.Job, live.session.Character.Level = 530, 30
	live.session.Character.Skills = []domain.SkillState{{ID: 5301001, Level: 20}}
	seedBoundScene(t, s, live, sinks[0])
	return s, live, sinks[0]
}

func barrelPaidResult(q domain.CombatAttackRequest) domain.CombatAttackResult {
	r := fakeSharedResult(q)
	for i := range r.Characters {
		if r.Characters[i].ID == q.Session.CharacterID {
			r.Characters[i].MP -= q.Cost.MP
		}
	}
	return r
}

func TestBarrelReleaseDeduplicatesNativeKeyWithoutSuppressingNewCast(t *testing.T) {
	s, live, sink := barrelTestFixture(t)
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return barrelPaidResult(q), nil
	}}
	body := barrelTestRelease(live, 1)
	s.throwGrenade(context.Background(), 1, live, sink, body)
	s.throwGrenade(context.Background(), 1, live, sink, body)
	changed := append([]byte(nil), body...)
	changed[0]++ // Same native key with changed coordinates is still no new debit.
	s.throwGrenade(context.Background(), 1, live, sink, changed)
	if calls != 1 || live.session.Character.MP != 72 {
		t.Fatalf("duplicate paid twice: calls=%d MP=%d", calls, live.session.Character.MP)
	}
	s.throwGrenade(context.Background(), 1, live, sink, barrelTestRelease(live, 2))
	if calls != 2 || live.session.Character.MP != 44 {
		t.Fatalf("distinct native cast rejected: calls=%d MP=%d", calls, live.session.Character.MP)
	}
}

func TestBarrelReleaseUnknownOutcomeResolvesSameOperationOnly(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			s, live, sink := barrelTestFixture(t)
			var request domain.CombatAttackRequest
			var result domain.CombatAttackResult
			available, calls := false, 0
			repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				request, result = q, barrelPaidResult(q)
				return domain.CombatAttackResult{}, errors.New("reply lost")
			}, outcome: func(op, hash string) (*domain.CombatAttackResult, error) {
				if op != request.OperationID || hash != result.ManifestHash {
					t.Fatal("outcome lookup changed the operation")
				}
				if !available {
					return nil, errors.New("lookup unavailable")
				}
				if !committed {
					return nil, nil
				}
				return &result, nil
			}}
			s.repo = repo
			body := barrelTestRelease(live, 7)
			s.throwGrenade(context.Background(), 1, live, sink, body)
			s.throwGrenade(context.Background(), 1, live, sink, body)
			s.throwGrenade(context.Background(), 1, live, sink, barrelTestRelease(live, 8))
			if calls != 1 || live.session.Character.MP != 100 || len(s.gameplay.pendingCombat) != 1 {
				t.Fatalf("unknown command guessed or re-executed: calls=%d MP=%d", calls, live.session.Character.MP)
			}
			available = true
			s.resolveCombatSettlements(context.Background(), repo)
			want := 100
			if committed {
				want = 72
			}
			if calls != 1 || live.session.Character.MP != want || len(s.gameplay.pendingCombat) != 0 {
				t.Fatalf("resolve: calls=%d MP=%d pending=%d", calls, live.session.Character.MP, len(s.gameplay.pendingCombat))
			}
			repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return barrelPaidResult(q), nil
			}
			s.throwGrenade(context.Background(), 1, live, sink, body)
			if calls != 1 {
				t.Fatal("resolved key was executed again")
			}
			s.throwGrenade(context.Background(), 1, live, sink, barrelTestRelease(live, 8))
			if calls != 2 || live.session.Character.MP != want-28 {
				t.Fatal("known resolution did not unblock a new release")
			}
		})
	}
}

type barrelReplacementRepo struct {
	*sharedCombatRepo
	before func()
}

func (r *barrelReplacementRepo) CombatParties(ctx context.Context, lease domain.WorldLease, ids []int64) ([]domain.CombatPartyPin, error) {
	if r.before != nil {
		r.before()
		r.before = nil
	}
	return r.sharedCombatRepo.CombatParties(ctx, lease, ids)
}

func TestBarrelReleaseCannotChargeReplacementEntry(t *testing.T) {
	s, live, sink := barrelTestFixture(t)
	calls := 0
	repo := &barrelReplacementRepo{sharedCombatRepo: &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return barrelPaidResult(q), nil
	}}}
	repo.before = func() {
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[mapKey{channel: 1, mapID: live.session.Character.MapID}]
		replacement := *room.members[live.session.CharacterID]
		room.members[live.session.CharacterID] = &replacement
		s.gameplay.mu.Unlock()
	}
	s.repo = repo
	s.throwGrenade(context.Background(), 1, live, sink, barrelTestRelease(live, 1))
	if calls != 0 || live.session.Character.MP != 100 {
		t.Fatal("same-live same-map replacement was charged for an old visit")
	}
}

func TestBarrelReleaseConcurrentDuplicateCannotExecuteWhileFirstPending(t *testing.T) {
	s, live, sink := barrelTestFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		close(entered)
		<-release
		return barrelPaidResult(q), nil
	}}
	body := barrelTestRelease(live, 1)
	first, duplicate := make(chan struct{}), make(chan struct{})
	go func() {
		s.throwGrenade(context.Background(), 1, live, sink, body)
		close(first)
	}()
	<-entered
	go func() {
		s.throwGrenade(context.Background(), 1, live, sink, body)
		close(duplicate)
	}()
	close(release)
	for _, done := range []chan struct{}{first, duplicate} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("duplicate release deadlocked")
		}
	}
	if calls != 1 || live.session.Character.MP != 72 {
		t.Fatalf("concurrent duplicate debit: calls=%d MP=%d", calls, live.session.Character.MP)
	}
}

func TestBarrelReleaseLateReceiptCannotInstallPaidRunInNewVisit(t *testing.T) {
	s, live, sink := barrelTestFixture(t)
	var stored domain.CombatAttackResult
	ready := false
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		stored = barrelPaidResult(q)
		return domain.CombatAttackResult{}, errors.New("unknown commit")
	}, outcome: func(string, string) (*domain.CombatAttackResult, error) {
		if !ready {
			return nil, errors.New("unavailable")
		}
		return &stored, nil
	}}
	s.repo = repo
	s.throwGrenade(context.Background(), 1, live, sink, barrelTestRelease(live, 1))
	member := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].members[live.session.CharacterID]
	run := member.barrelRelease
	// Same live, socket, map and owner; only the immutable scene visit changes.
	seedBoundScene(t, s, live, sink)
	ready = true
	s.resolveCombatSettlements(context.Background(), repo)
	if run.paid {
		t.Fatal("late receipt installed an old projectile in a new visit")
	}
}

func TestBarrelReleaseRejectsInvalidNativeContextBeforeDebit(t *testing.T) {
	for _, bad := range []string{"unlearned", "wrong_job", "dead", "far", "negative_key", "trailing", "facing"} {
		t.Run(bad, func(t *testing.T) {
			s, live, sink := barrelTestFixture(t)
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return barrelPaidResult(q), nil
			}}
			body := barrelTestRelease(live, 1)
			switch bad {
			case "unlearned":
				live.session.Character.Skills = nil
			case "wrong_job":
				live.session.Character.Job = 200
			case "dead":
				live.session.Character.HP = 0
			case "far":
				body[0], body[1] = 0xff, 0x7f
			case "negative_key":
				body = barrelTestRelease(live, -1)
			case "trailing":
				body = append(body, 0)
			case "facing":
				body[20] = 2
			}
			s.throwGrenade(context.Background(), 1, live, sink, body)
			if calls != 0 || live.session.Character.MP != 100 {
				t.Fatal("invalid release reached debit")
			}
		})
	}
}
