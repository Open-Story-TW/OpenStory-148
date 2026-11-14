package world

import (
	"context"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
	"time"
)

func pendingVitalsFixture(t *testing.T, family domain.CombatFamily) (*Server, []*liveCharacter, []*gameplayTestSink, *sharedCombatRepo, *gameplayMob, *domain.CombatAttackResult) {
	t.Helper()
	s, lives, sinks, mob := sharedWorldFixture(t)
	s.skills = newSkillSpace()
	for i, live := range lives {
		live.session.Character.Job = 0
		if i == 0 && family == domain.CombatPhysicalSkill {
			live.session.Character.Job = 100
		}
		if i == 0 && family == domain.CombatThreeSnails {
			live.session.Character.Skills = []domain.SkillState{{ID: 1000, Level: 1}}
		}
		if i == 1 {
			live.session.Character.MP = 80
		}
		seedBoundScene(t, s, live, sinks[i])
		if err := s.enterSkills(context.Background(), 1, live, sinks[i]); err != nil {
			t.Fatal(err)
		}
		s.skills.members[live.session.CharacterID].lastRegen = time.Time{}
		s.skills.members[live.session.CharacterID].recoveryPose = recoveryPose{visit: s.sceneFor(live, sinks[i]), known: true, stable: true, grounded: true, x: live.session.Character.X, y: live.session.Character.Y, state: 4}
	}
	mob.life.X = 80
	var committed domain.CombatAttackResult
	lookups := 0
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		committed = fakeSharedResult(q)
		if family == domain.CombatThreeSnails {
			for i := range committed.Characters {
				if committed.Characters[i].ID == 1 {
					committed.Characters[i].MP -= 3
				}
			}
		}
		return domain.CombatAttackResult{}, errors.New("reply lost after commit")
	}
	repo.outcome = func(_, _ string) (*domain.CombatAttackResult, error) {
		lookups++
		if lookups == 1 {
			return nil, errors.New("receipt lookup unavailable")
		}
		return &committed, nil
	}
	s.repo = repo
	var err error
	if family == domain.CombatThreeSnails {
		mob.hp = 5
		err = s.magicGameplay(context.Background(), 1, lives[0], sinks[0], worldMagic(t, mob.objectID, 10, 0, 0))
	} else {
		skill := 0
		if family == domain.CombatPhysicalSkill {
			skill = 1001005
		}
		err = s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Skill: skill, Tick: 71, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, family)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(s.gameplay.pendingCombat) != 1 {
		t.Fatal("did not retain unknown commit")
	}
	return s, lives, sinks, repo, mob, &committed
}

// The pre-fix counterexample used a genuine takeDamage(3) while the ticket was
// unresolved: HP50 -> 47 -> receipt50. New policy rejects transient commands
// until resolution; it does not try to reconstruct capped healing by net delta.
func TestUnknownCombatCommitDefersTransientVitalsWithoutConsumingThrottles(t *testing.T) {
	for _, family := range []domain.CombatFamily{domain.CombatNormal, domain.CombatPhysicalSkill, domain.CombatThreeSnails} {
		t.Run(string(rune('0'+family)), func(t *testing.T) {
			s, lives, sinks, repo, mob, _ := pendingVitalsFixture(t, family)
			for i, live := range lives {
				before := live.session.Character
				member := s.skills.members[before.ID]
				damageAt, regenAt := member.lastDamage, member.lastRegen
				if err := s.takeDamage(context.Background(), live, sinks[i], protocol.PlayerDamage{Tick: 72, ObjectID: mob.objectID, MonsterID: mob.stats.ID, Damage: 3}); err != nil {
					t.Fatal(err)
				}
				if err := s.regenerate(context.Background(), live, sinks[i], protocol.Regeneration{Opcode: protocol.RecvRegeneration, Tick: 72, HP: 10, MP: 3}); err != nil {
					t.Fatal(err)
				}
				c := live.session.Character
				if c.HP != before.HP || c.MP != before.MP || live.dirty {
					t.Fatal("unknown receipt accepted a transient vital change")
				}
				if member.damageSeen || member.lastDamage != damageAt || member.lastRegen != regenAt {
					t.Fatal("rejected pending command consumed a throttle")
				}
			}
			s.resolveCombatSettlements(context.Background(), repo)
			cost := 0
			if family == domain.CombatPhysicalSkill {
				cost = 6
			}
			if family == domain.CombatThreeSnails {
				cost = 3
			}
			c := lives[0].session.Character
			if c.HP != 50 || c.MP != 100-cost || c.EXP != 3 || c.Revision != 2 || len(s.gameplay.pendingCombat) != 0 {
				t.Fatalf("lost committed cost/reward or retained gate: %+v", c)
			}
			// A different live mob can now hurt the actor. No rejected hit is replayed.
			incoming := *mob
			incoming.objectID++
			incoming.hp = 15
			incoming.stats.PAD = 10
			s.gameplay.rooms[mapKey{1, 40000}].mobs[incoming.objectID] = &incoming
			if err := s.takeDamage(context.Background(), lives[0], sinks[0], protocol.PlayerDamage{Tick: 72, ObjectID: incoming.objectID, MonsterID: incoming.stats.ID, Damage: 3}); err != nil {
				t.Fatal(err)
			}
			if lives[0].session.Character.HP != 47 || !lives[0].dirty {
				t.Fatal("resolved gate still blocked the unconsumed damage tick")
			}
			if err := s.regenerate(context.Background(), lives[1], sinks[1], protocol.Regeneration{Opcode: protocol.RecvRegeneration, Tick: 72, HP: 10, MP: 3}); err != nil {
				t.Fatal(err)
			}
			if lives[1].session.Character.HP != 60 || lives[1].session.Character.MP != 83 {
				t.Fatal("reward peer regeneration did not resume")
			}
			if family == domain.CombatThreeSnails {
				if err := s.regenerate(context.Background(), lives[0], sinks[0], protocol.Regeneration{Opcode: protocol.RecvRegeneration, Tick: 72, MP: 3}); err != nil {
					t.Fatal(err)
				}
				if lives[0].session.Character.MP != 100 {
					t.Fatal("post-resolution regeneration lost committed MP headroom")
				}
			}
			s.resolveCombatSettlements(context.Background(), repo)
			if lives[0].session.Character.HP != 47 || sinks[0].count(protocol.SendKillMonster) != 1 {
				t.Fatal("resolution replayed a transient event or settlement")
			}
		})
	}
}

func TestUnknownCombatRollbackReleasesAllTransientVitalGates(t *testing.T) {
	s, lives, sinks, repo, mob, _ := pendingVitalsFixture(t, domain.CombatPhysicalSkill)
	repo.outcome = func(string, string) (*domain.CombatAttackResult, error) { return nil, nil }
	s.resolveCombatSettlements(context.Background(), repo)
	if len(s.gameplay.pendingCombat) != 0 || mob.claiming {
		t.Fatal("proven rollback retained gate")
	}
	for i, live := range lives {
		if err := s.takeDamage(context.Background(), live, sinks[i], protocol.PlayerDamage{Tick: 72, ObjectID: mob.objectID, MonsterID: mob.stats.ID, Damage: 3}); err != nil {
			t.Fatal(err)
		}
		if live.session.Character.HP != 47 || live.session.Character.EXP != 0 || live.session.Character.Revision != 1 {
			t.Fatal("rollback guessed progress or retained vital gate")
		}
	}
}

func TestUnknownCombatGatePublishedBeforePeerLocksRelease(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		return domain.CombatAttackResult{}, errors.New("unknown commit")
	}, outcome: func(string, string) (*domain.CombatAttackResult, error) {
		close(entered)
		<-release
		return nil, errors.New("unknown receipt")
	}}
	s.repo = repo
	go func() {
		done <- s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 73, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal)
	}()
	<-entered
	s.gameplay.mu.Lock()
	close(release)
	acquired := make(chan struct{})
	go func() { lives[0].persistMu.Lock(); close(acquired); lives[0].persistMu.Unlock() }()
	select {
	case <-acquired:
		s.gameplay.mu.Unlock()
		t.Fatal("peer lock released before pending gate could be published")
	case <-time.After(50 * time.Millisecond):
	}
	s.gameplay.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending registration deadlocked")
	}
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("peer lock did not release")
	}
	lives[0].persistMu.Lock()
	pending := s.combatVitalsPending(lives[0], lives[0].session)
	lives[0].persistMu.Unlock()
	if !pending {
		t.Fatal("waiter acquired peer without a published gate")
	}
}

// This is the exact helper called by runSkills after its due-recovery check.
type pendingVitalsTickRepo struct{ calls int }

func (*pendingVitalsTickRepo) AllocateSkill(context.Context, domain.SkillAllocateRequest) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (*pendingVitalsTickRepo) CastSkill(context.Context, domain.SkillCastRequest) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (*pendingVitalsTickRepo) CancelSkill(context.Context, domain.CharacterSession, int) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (r *pendingVitalsTickRepo) TickSkillBuffs(_ context.Context, q domain.CharacterSession) (domain.SkillResult, error) {
	r.calls++
	return domain.SkillResult{Character: q.Character}, nil
}
func TestUnknownCombatRecoveryWorkerSkipsEveryFrozenPeer(t *testing.T) {
	s, lives, _, repo, _, _ := pendingVitalsFixture(t, domain.CombatThreeSnails)
	ticks := &pendingVitalsTickRepo{}
	for _, live := range lives {
		live.mu.Lock()
		live.session.Character.SkillBuffs = []domain.SkillBuff{{ID: 1001, Stat: "RECOVERY", Value: 15, ExpiresAt: time.Now().Add(time.Minute), NextTick: time.Now().Add(-time.Second)}}
		live.mu.Unlock()
		s.tickSkillMember(context.Background(), ticks, s.skills.members[live.session.CharacterID])
	}
	if ticks.calls != 0 {
		t.Fatal("pending recovery reached repository")
	}
	s.resolveCombatSettlements(context.Background(), repo)
	for _, live := range lives {
		s.tickSkillMember(context.Background(), ticks, s.skills.members[live.session.CharacterID])
	}
	if ticks.calls != 2 {
		t.Fatal("recovery did not resume after original receipt resolution")
	}
}
