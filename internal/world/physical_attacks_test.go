package world

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type physicalTestRepo struct {
	domain.Repository
	apply func(domain.PhysicalAttackRequest) (domain.SkillAttackResult, error)
}

func (r physicalTestRepo) ApplyPhysicalSkillAttack(_ context.Context, q domain.PhysicalAttackRequest) (domain.SkillAttackResult, error) {
	return r.apply(q)
}
func (r physicalTestRepo) CombatParties(context.Context, domain.WorldLease, []int64) ([]domain.CombatPartyPin, error) {
	return nil, nil
}
func (r physicalTestRepo) CombatAttackOutcome(context.Context, domain.WorldLease, string, string) (*domain.CombatAttackResult, error) {
	return nil, nil
}
func (r physicalTestRepo) ApplyCombatAttack(_ context.Context, q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	old := domain.PhysicalAttackRequest{Session: q.Session, OperationID: q.OperationID, ClientTick: q.ClientTick, SkillID: q.SkillID}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return domain.CombatAttackResult{}, err
	}
	for _, t := range q.Targets {
		target := domain.PhysicalAttackTarget{ObjectID: t.ObjectID, MobID: t.MobID, SpawnOperationID: t.SpawnOperationID, X: t.X, Y: t.Y, Damage: t.Damage, Defeated: t.Defeated}
		if t.Defeated {
			mob, e := catalog.Mob(t.MobID)
			if e != nil {
				return domain.CombatAttackResult{}, e
			}
			target.Experience = mob.EXP
		}
		old.Targets = append(old.Targets, target)
	}
	result, err := r.apply(old)
	if err != nil {
		return domain.CombatAttackResult{}, err
	}
	return domain.CombatAttackResult{Characters: []domain.Character{result.Character}, Replayed: result.Replayed}, nil
}

func physicalWorldFixture(t *testing.T) (*Server, *liveCharacter, []*gameplayMob, []*gameplayTestSink) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: g, online: map[int64]domain.CharacterSession{}}
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, SessionID: "physical-world-session", Generation: 1, Character: domain.Character{ID: 1, MapID: 40000, Level: 10, Job: 100, HP: 50, MaxHP: 50, MP: 100, MaxMP: 100, STR: 50, DEX: 4, Revision: 1, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 20}}, Skills: []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}}}}
	b := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, HP: 50}}}
	sinks := []*gameplayTestSink{{}, {}}
	if err = s.enterGameplay(1, a, sinks[0]); err != nil {
		t.Fatal(err)
	}
	if err = s.enterGameplay(1, b, sinks[1]); err != nil {
		t.Fatal(err)
	}
	room := g.rooms[mapKey{channel: 1, mapID: 40000}]
	var source *gameplayMob
	for _, mob := range room.mobs {
		source = mob
		break
	}
	if source == nil {
		t.Fatal("starter map has no authoritative mob")
	}
	room.mobs = map[int32]*gameplayMob{}
	var targets []*gameplayMob
	for i := 0; i < 2; i++ {
		mob := *source
		mob.objectID = g.alloc()
		mob.hp = mob.stats.HP
		mob.life.X, mob.life.Y = 0, 0
		room.mobs[mob.objectID] = &mob
		targets = append(targets, &mob)
	}
	return s, a, targets, sinks
}

func TestPhysicalSkillTwoPlayersObserveAtomicMultiKill(t *testing.T) {
	s, a, mobs, sinks := physicalWorldFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.repo = physicalTestRepo{apply: func(q domain.PhysicalAttackRequest) (domain.SkillAttackResult, error) {
		calls.Add(1)
		if len(q.Targets) != 2 || !q.Targets[0].Defeated || !q.Targets[1].Defeated || q.Targets[0].Damage > 100 || q.Targets[1].Damage > 100 {
			t.Errorf("world trusted client damage or lost targets: %+v", q.Targets)
		}
		close(entered)
		<-release
		c := q.Session.Character
		c.MP -= 6
		c.Revision++
		for _, target := range q.Targets {
			c.EXP += target.Experience
		}
		a.mu.Lock()
		a.session.Character.X = 55
		a.dirty = true
		a.mu.Unlock()
		return domain.SkillAttackResult{Character: c}, nil
	}}
	attack := protocol.MeleeAttack{Skill: 1001005, Tick: 1, Display: 35, Speed: 6, Targets: []protocol.MeleeTarget{{ObjectID: mobs[0].objectID, Damage: 100000}, {ObjectID: mobs[1].objectID, Damage: 100000}}}
	done := make(chan error, 1)
	go func() { done <- s.physicalSkillGameplay(context.Background(), 1, a, sinks[0], attack) }()
	<-entered
	// A claimed spawn is unavailable to a simultaneous ordinary hit by a peer.
	peer := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, Job: 0, HP: 50, STR: 100, DEX: 10, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11, WATK: 100}}}}}
	if err := s.meleeGameplay(context.Background(), 1, peer, sinks[1], protocol.MeleeAttack{Tick: 2, Targets: []protocol.MeleeTarget{{ObjectID: mobs[0].objectID, Damage: 100000}}}); err != nil {
		t.Fatal(err)
	}
	if mobs[0].hp != mobs[0].stats.HP || mobs[1].hp != mobs[1].stats.HP {
		t.Fatal("mob HP changed before database commit")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || a.session.Character.MP != 94 || a.session.Character.X != 55 || !a.dirty {
		t.Fatal("physical transaction cost/merge failed")
	}
	for _, sink := range sinks {
		if sink.count(protocol.SendKillMonster) != 2 {
			t.Fatal("two-player observer missed multi-kill deaths")
		}
	}
	if sinks[0].count(protocol.SendMeleeAttack) != 0 || sinks[1].count(protocol.SendMeleeAttack) != 1 {
		t.Fatal("foreign physical relay actor boundary incorrect")
	}
	if err := s.physicalSkillGameplay(context.Background(), 1, a, sinks[0], attack); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || a.session.Character.MP != 94 {
		t.Fatal("same native tick charged twice")
	}
}

func TestPhysicalSkillValidatesAllTargetsAndRollsBackFailedCommit(t *testing.T) {
	s, a, mobs, sinks := physicalWorldFixture(t)
	calls := 0
	s.repo = physicalTestRepo{apply: func(q domain.PhysicalAttackRequest) (domain.SkillAttackResult, error) {
		calls++
		return domain.SkillAttackResult{}, errors.New("database unavailable")
	}}
	attack := protocol.MeleeAttack{Skill: 1001005, Tick: 1, Targets: []protocol.MeleeTarget{{ObjectID: mobs[0].objectID, Damage: 5}, {ObjectID: mobs[1].objectID, Damage: 5}}}
	mobs[1].life.X = 10000
	if err := s.physicalSkillGameplay(context.Background(), 1, a, sinks[0], attack); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || mobs[0].claiming || mobs[0].hp != mobs[0].stats.HP {
		t.Fatal("partial target validation deducted/claimed the first target")
	}
	mobs[1].life.X = 0
	attack.Tick++
	if err := s.physicalSkillGameplay(context.Background(), 1, a, sinks[0], attack); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || a.session.Character.MP != 100 || a.session.Character.Revision != 1 || mobs[0].claiming || mobs[1].claiming || mobs[0].hp != mobs[0].stats.HP || mobs[1].hp != mobs[1].stats.HP {
		t.Fatal("database failure escaped into mob/vitals state")
	}
	for _, sink := range sinks {
		if sink.count(protocol.SendDamageMonster) != 0 || sink.count(protocol.SendKillMonster) != 0 {
			t.Fatal("uncommitted attack was published")
		}
	}
}
