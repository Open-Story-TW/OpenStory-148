package world

import (
	"context"
	"errors"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

type partyBuffWorldRepo struct {
	domain.Repository
	apply   func(domain.PartySkillCastRequest) (domain.PartySkillCastResult, error)
	outcome func(string, string) (*domain.PartySkillCastResult, error)
	cancels int
}

func (*partyBuffWorldRepo) ReadParty(context.Context, domain.CharacterSession) (domain.PartyState, error) {
	return domain.PartyState{Party: domain.PartySnapshot{ID: 1, Revision: 1, Members: []domain.PartyMember{{CharacterID: 1}, {CharacterID: 2}}}}, nil
}
func (*partyBuffWorldRepo) MutateParty(context.Context, domain.PartyRequest) (domain.PartyMutation, error) {
	return domain.PartyMutation{}, domain.ErrForbidden
}
func (r *partyBuffWorldRepo) ApplyPartySkillCast(_ context.Context, q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
	return r.apply(q)
}
func (r *partyBuffWorldRepo) PartySkillCastOutcome(_ context.Context, _ domain.WorldLease, op, hash string) (*domain.PartySkillCastResult, error) {
	if r.outcome == nil {
		return nil, nil
	}
	return r.outcome(op, hash)
}
func (*partyBuffWorldRepo) AllocateSkill(context.Context, domain.SkillAllocateRequest) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (*partyBuffWorldRepo) CastSkill(context.Context, domain.SkillCastRequest) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (r *partyBuffWorldRepo) CancelSkill(_ context.Context, q domain.CharacterSession, id int) (domain.SkillResult, error) {
	r.cancels++
	c := cloneCombatSession(q).Character
	var expired, kept []domain.SkillBuff
	for _, b := range c.SkillBuffs {
		if b.ID == id {
			expired = append(expired, b)
		} else {
			kept = append(kept, b)
		}
	}
	c.SkillBuffs = kept
	c.Revision++
	return domain.SkillResult{Character: c, Changed: true, Expired: expired}, nil
}
func (*partyBuffWorldRepo) TickSkillBuffs(context.Context, domain.CharacterSession) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func partyBuffWorldFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, *partyBuffWorldRepo) {
	t.Helper()
	s, lives, sinks, _ := sharedWorldFixture(t)
	s.skills = newSkillSpace()
	for i, live := range lives {
		c := &live.session.Character
		c.AccountID = live.session.AccountID
		c.WorldID = live.session.Lease.WorldID
		c.Level = 30
		c.VitalsModel = 1
		c.BaseMaxHP, c.BaseMaxMP, c.HP, c.MP = 100, 101, 50, 50
		if i == 0 {
			c.Job = 130
			c.BaseMaxHP, c.BaseMaxMP, c.HP, c.MP = 411, 100, 100, 100
			c.Skills = []domain.SkillState{{ID: 1000006, Level: 10}, {ID: 1301006, Level: 3}, {ID: 1301007, Level: 20}}
		} else {
			c.Job = 200
			c.Skills = []domain.SkillState{{ID: 2000006, Level: 10}}
		}
		var err error
		c.MaxHP, c.MaxMP, err = content.DerivedVitals(*c, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		seedBoundScene(t, s, live, sinks[i])
		if err = s.enterSkills(context.Background(), 1, live, sinks[i]); err != nil {
			t.Fatal(err)
		}
	}
	repo := &partyBuffWorldRepo{}
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		return fakePartyBuffResult(t, q), nil
	}
	s.repo = repo
	return s, lives, sinks, repo
}
func fakePartyBuffResult(t *testing.T, q domain.PartySkillCastRequest) domain.PartySkillCastResult {
	t.Helper()
	hash, _ := domain.PartySkillManifestHash(q)
	r := domain.PartySkillCastResult{OperationID: q.OperationID, ManifestHash: hash}
	plan, e := skills.PartyCast("", q.Session.Character, q.SkillID, q.ClientLevel, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	expiry := time.Now().Add(plan.Party.Duration)
	stats := map[string]bool{}
	for _, v := range plan.Party.Stats {
		stats[string(v.Stat)] = true
	}
	for _, m := range q.Members {
		c := cloneCombatSession(m).Character
		if c.ID == q.Session.CharacterID {
			c.MP -= plan.MPCost
		}
		var old, kept []domain.SkillBuff
		for _, b := range c.SkillBuffs {
			if stats[b.Stat] || b.ID == q.SkillID {
				old = append(old, b)
			} else {
				kept = append(kept, b)
			}
		}
		for _, v := range plan.Party.Stats {
			kept = append(kept, domain.SkillBuff{ID: q.SkillID, Level: q.ClientLevel, Stat: string(v.Stat), Value: v.Value, ExpiresAt: expiry})
		}
		c.SkillBuffs = kept
		c.MaxHP, c.MaxMP, e = content.DerivedVitals(c, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		c.HP, c.MP = min(c.HP, c.MaxHP), min(c.MP, c.MaxMP)
		c.Revision++
		r.Recipients = append(r.Recipients, domain.PartySkillRecipient{Session: m, Character: c, Expired: old})
	}
	return r
}
func TestHyperBodyWorldTwoRecipientsAtomicBenefitAndLateEntryState(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	calls := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		lives[0].mu.Lock()
		lives[0].session.Character.X = 25
		lives[0].dirty = true
		lives[0].mu.Unlock()
		return fakePartyBuffResult(t, q), nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 20, SkillID: 1301007, Level: 20, HasPosition: true}); e != nil {
		t.Fatal(e)
	}
	a, b := lives[0].session.Character, lives[1].session.Character
	if calls != 1 || a.MaxHP != 739 || a.MaxMP != 160 || a.MP != 40 || a.HP != 100 || b.MaxHP != 160 || b.MaxMP != 181 || b.MP != 50 || a.X != 25 || !lives[0].dirty {
		t.Fatalf("wrong authoritative benefits:%+v/%+v", a, b)
	}
	if sinks[0].count(0x2d) != 1 || sinks[1].count(0x2d) != 1 || sinks[0].count(0x154) != 0 || sinks[1].count(0x154) != 1 {
		t.Fatal("local paired buffs or recipient-only type2 mismatch")
	}
	late := &liveCharacter{session: domain.CharacterSession{CharacterID: 3, ChannelID: 1, Character: domain.Character{ID: 3, MapID: 40000, HP: 50}}}
	lateSink := &gameplayTestSink{}
	if e := s.enterSkills(context.Background(), 1, late, lateSink); e != nil {
		t.Fatal(e)
	}
	// One remote packet per character carries both MAXHP and MAXMP bits.
	if lateSink.count(0x145) != 2 || lateSink.count(0x144) != 0 {
		t.Fatal("late entry must receive one payloadless state per recipient without replayed cast flashes")
	}
	for _, p := range buffPackets(lateSink) {
		if len(p) >= 2 && p[0] == 0x45 && p[1] == 1 && len(p) != 2+4+32+3+2 {
			t.Fatal("late entry foreign percentage packet has payload")
		}
	}
	s.leaveSkills(3, late)
	again := &gameplayTestSink{}
	if e := s.enterSkills(context.Background(), 1, late, again); e != nil {
		t.Fatal(e)
	}
	if again.count(0x145) != 2 || again.count(0x144) != 0 {
		t.Fatal("reentry lost state or replayed transient FX")
	}
}
func TestHyperBodyUnknownCommitGatesVitalsAndResolvesOriginalOnly(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	var committed domain.PartySkillCastResult
	calls, lookups := 0, 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		committed = fakePartyBuffResult(t, q)
		return domain.PartySkillCastResult{}, errors.New("commit reply lost")
	}
	repo.outcome = func(_, hash string) (*domain.PartySkillCastResult, error) {
		lookups++
		if lookups == 1 {
			return nil, errors.New("lookup down")
		}
		if hash != committed.ManifestHash {
			t.Fatal("resolver changed manifest")
		}
		return &committed, nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 21, SkillID: 1301007, Level: 20, HasPosition: true}); e != nil {
		t.Fatal(e)
	}
	if len(s.skills.partyPending) != 1 || lives[0].session.Character.MP != 100 {
		t.Fatal("unknown outcome guessed committed cost")
	}
	for i, live := range lives {
		if !s.gmBusy(live) {
			t.Fatal("unknown party receipt did not block QA mutation")
		}
		before := live.session.Character
		if e := s.takeDamage(context.Background(), live, sinks[i], protocol.PlayerDamage{Tick: 55, Damage: 3}); e != nil {
			t.Fatal(e)
		}
		if live.session.Character.HP != before.HP || s.skills.members[before.ID].damageSeen {
			t.Fatal("pending party cast accepted damage or throttle")
		}
	}
	s.resolvePartySkills(context.Background(), repo)
	if calls != 1 || len(s.skills.partyPending) != 0 || lives[0].session.Character.MP != 40 || lives[1].session.Character.MaxMP != 181 {
		t.Fatal("same-op resolution lost cost or recipient benefit")
	}
	count := sinks[1].count(0x2d)
	s.resolvePartySkills(context.Background(), repo)
	if calls != 1 || sinks[1].count(0x2d) != count {
		t.Fatal("resolved operation repeated")
	}
}
func TestHyperBodyStaleRecipientAndMalformedResultNeverOverwrite(t *testing.T) {
	for _, kind := range []string{"replacement", "revision", "malformed", "derived"} {
		t.Run(kind, func(t *testing.T) {
			s, lives, sinks, repo := partyBuffWorldFixture(t)
			repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
				r := fakePartyBuffResult(t, q)
				if kind == "replacement" {
					replacement := &liveCharacter{session: lives[1].session}
					replacement.session.Generation++
					seedBoundScene(t, s, replacement, &gameplayTestSink{})
				} else if kind == "revision" {
					lives[1].mu.Lock()
					lives[1].session.Character.Revision += 2
					lives[1].mu.Unlock()
				} else if kind == "derived" {
					r.Recipients[1].Character.Equipment = []domain.Equipment{{Slot: -5, HP: -10000}}
				} else {
					r.Recipients[1].Session.Generation++
				}
				return r, nil
			}
			e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 22, SkillID: 1301007, Level: 20, HasPosition: true})
			if kind == "malformed" || kind == "derived" {
				expected := domain.ErrForbidden
				if kind == "derived" {
					expected = domain.ErrInvalidInput
				}
				if !errors.Is(e, expected) || lives[0].session.Character.MP != 100 || sinks[0].count(0x2d) != 0 {
					t.Fatal("malformed later row partially mutated caster", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if len(lives[1].session.Character.SkillBuffs) != 0 || lives[1].session.Character.MaxMP != 121 || sinks[1].count(0x2d) != 0 {
				t.Fatal("stale recipient received old result")
			}
		})
	}
}

func TestHyperBodyPendingCancelDoesNotCallRepositoryAndResumesAfterResolve(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	old := domain.SkillBuff{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}
	lives[0].session.Character.SkillBuffs = []domain.SkillBuff{old}
	var saved domain.PartySkillCastResult
	lookups := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		saved = fakePartyBuffResult(t, q)
		return domain.PartySkillCastResult{}, errors.New("unknown commit")
	}
	repo.outcome = func(string, string) (*domain.PartySkillCastResult, error) {
		lookups++
		if lookups == 1 {
			return nil, errors.New("unknown outcome")
		}
		return &saved, nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 70, SkillID: 1301007, Level: 20, HasPosition: true}); e != nil {
		t.Fatal(e)
	}
	if e := s.cancelSkill(context.Background(), lives[0], sinks[0], 1001003); e != nil {
		t.Fatal(e)
	}
	if repo.cancels != 0 || len(lives[0].session.Character.SkillBuffs) != 1 || sinks[0].count(0x2e) != 0 {
		t.Fatal("pending cancel altered old buff or called new transaction")
	}
	s.resolvePartySkills(context.Background(), repo)
	if e := s.cancelSkill(context.Background(), lives[0], sinks[0], 1001003); e != nil {
		t.Fatal(e)
	}
	if repo.cancels != 1 || len(lives[0].session.Character.SkillBuffs) != 2 || sinks[0].count(0x2e) != 1 || lives[0].session.Character.MP != 40 {
		t.Fatal("cancel gate did not resume or lost committed MP")
	}
}
func TestHyperBodyKnownRollbackReleasesVitalAndCancelGates(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	repo.apply = func(domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		return domain.PartySkillCastResult{}, errors.New("unknown original")
	}
	count := 0
	repo.outcome = func(string, string) (*domain.PartySkillCastResult, error) {
		count++
		if count == 1 {
			return nil, errors.New("lookup down")
		}
		return nil, nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 71, SkillID: 1301007, Level: 20, HasPosition: true}); e != nil {
		t.Fatal(e)
	}
	s.resolvePartySkills(context.Background(), repo)
	for _, live := range lives {
		live.persistMu.Lock()
		pending := s.combatVitalsPending(live, live.session)
		live.persistMu.Unlock()
		if pending {
			t.Fatal("rollback left peer gated")
		}
	}
	if len(s.skills.partyPending) != 0 || lives[0].session.Character.MP != 100 || lives[0].session.Character.MaxHP != 493 || sinks[0].count(0x2d) != 0 {
		t.Fatal("rollback guessed partial cost/effect")
	}
}
func TestHyperBodyExpiredReceiptClampsWithoutReplayingFX(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	// Both peers already had a previous active cast and were healed before the
	// new request. The new immutable receipt itself never grants HP/MP.
	oldExpiry := time.Now().Add(time.Minute)
	for _, live := range lives {
		c := &live.session.Character
		c.SkillBuffs = []domain.SkillBuff{{ID: 1301007, Level: 20, Stat: "MAXHP", Value: 60, ExpiresAt: oldExpiry}, {ID: 1301007, Level: 20, Stat: "MAXMP", Value: 60, ExpiresAt: oldExpiry}}
		var e error
		c.MaxHP, c.MaxMP, e = content.DerivedVitals(*c, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		c.HP = c.MaxHP
	}
	lives[1].session.Character.MP = lives[1].session.Character.MaxMP
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		r := fakePartyBuffResult(t, q)
		expiredAt := time.Now().Add(-time.Second)
		for i := range r.Recipients {
			for j := range r.Recipients[i].Character.SkillBuffs {
				r.Recipients[i].Character.SkillBuffs[j].ExpiresAt = expiredAt
			}
		}
		return r, nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 72, SkillID: 1301007, Level: 20, HasPosition: true}); e != nil {
		t.Fatal(e)
	}
	if lives[0].session.Character.HP != 493 || lives[0].session.Character.MP != 40 || !lives[0].dirty || lives[1].session.Character.HP != 100 || lives[1].session.Character.MP != 121 {
		t.Fatal("delayed expired receipt retained old maxima, lost committed cost or was not dirty")
	}
	if sinks[0].count(0x2d) != 0 || sinks[1].count(0x2d) != 0 || sinks[1].count(0x154) != 0 || sinks[0].count(0x144) != 0 {
		t.Fatal("expired receipt replayed temporary state/FX")
	}
}
func TestHyperBodyTwoSimultaneousCastersKeepSortedPeerLocks(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	lives[1].session.Character.Job = 130
	lives[1].session.Character.Skills = append([]domain.SkillState(nil), lives[0].session.Character.Skills...)
	lives[1].session.Character.MP = 100
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return fakePartyBuffResult(t, q), nil
	}
	done := make(chan error, 2)
	go func() {
		done <- s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 73, SkillID: 1301007, Level: 20, HasPosition: true})
	}()
	<-entered
	go func() {
		done <- s.castSkill(context.Background(), lives[1], sinks[1], protocol.SkillCast{Tick: 74, SkillID: 1301007, Level: 20, HasPosition: true})
	}()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("mutual party casts deadlocked")
		}
	}
	if calls != 2 || lives[0].session.Character.MP != 40 || lives[1].session.Character.MP != 40 {
		t.Fatal("simultaneous costs lost or duplicated")
	}
}

func TestHyperBodyOldCancelCannotClearNewerCast(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	q := protocol.SkillCast{Tick: 90, SkillID: 1301007, Level: 20, HasPosition: true}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], q); e != nil {
		t.Fatal(e)
	}
	old := cloneCombatSession(lives[0].session)
	oldBuffs := append([]domain.SkillBuff(nil), old.Character.SkillBuffs...)
	// Simulate already-authoritatively restored MP before a second genuine cast.
	lives[0].session.Character.MP = 100
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		r := fakePartyBuffResult(t, q)
		expiry := oldBuffs[0].ExpiresAt.Add(time.Second)
		for i := range r.Recipients {
			for j := range r.Recipients[i].Character.SkillBuffs {
				r.Recipients[i].Character.SkillBuffs[j].ExpiresAt = expiry
			}
		}
		return r, nil
	}
	q.Tick++
	if e := s.castSkill(context.Background(), lives[0], sinks[0], q); e != nil {
		t.Fatal(e)
	}
	s.publishSkillTick(old, sinks[0], domain.SkillResult{Expired: oldBuffs}, old.Character.HP)
	if sinks[0].count(0x2e) != 0 || sinks[1].count(0x146) != 0 || !lives[0].session.Character.SkillBuffs[0].ExpiresAt.After(oldBuffs[0].ExpiresAt) {
		t.Fatal("old cancellation cleared replacement percentage state")
	}
}

// Party skills outside the native mass-spell predicate (Time Leap) send no
// position; the cast still goes through using the server's position.
func TestPartySkillWithoutWirePositionStillCasts(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	calls := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		return fakePartyBuffResult(t, q), nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{Tick: 40, SkillID: 1301007, Level: 20}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || sinks[0].count(0x2d) != 1 {
		t.Fatalf("position-less party cast was dropped: calls %d", calls)
	}
}
