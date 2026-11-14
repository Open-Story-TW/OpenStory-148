package world

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type combatProgressGateSink struct {
	gameplayTestSink
	entered chan struct{}
	release chan struct{}
	op      uint16
}

func (s *combatProgressGateSink) WritePacket(p []byte) error {
	op := s.op
	if op == 0 {
		op = protocol.SendUpdateStats
	}
	if len(p) >= 2 && binary.LittleEndian.Uint16(p) == op {
		close(s.entered)
		<-s.release
	}
	return s.gameplayTestSink.WritePacket(p)
}

func TestCombatProgressPublicationRetainsPersistenceScope(t *testing.T) {
	s, lives, _, mob := sharedWorldFixture(t)
	key := mapKey{channel: 1, mapID: 40000}
	gate := &combatProgressGateSink{entered: make(chan struct{}), release: make(chan struct{})}
	s.gameplay.rooms[key].members[1].sink = gate
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	s.repo = repo
	done := make(chan struct{})
	go func() {
		_ = s.settledMeleeGameplay(context.Background(), 1, lives[0], gate, protocol.MeleeAttack{Tick: 101, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal)
		close(done)
	}()
	<-gate.entered
	if lives[0].persistMu.TryLock() {
		lives[0].persistMu.Unlock()
		t.Error("warp/checkpoint could overtake committed EXP publication")
	}
	close(gate.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("combat publication deadlocked")
	}
}

func TestCombatObserverExitReentryRejectsCapturedOldVisit(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	key := mapKey{channel: 1, mapID: 40000}
	ticket := &combatTicket{key: key, actor: lives[0], actorSession: cloneCombatSession(lives[0].session), sink: sinks[0],
		request: domain.CombatAttackRequest{Session: lives[0].session, Targets: []domain.CombatTarget{{ObjectID: mob.objectID, MobID: mob.stats.ID, BeforeHP: mob.hp, Damage: int(mob.hp), Defeated: true}}}, targets: []*gameplayMob{mob}}
	s.gameplay.mu.Lock()
	ticket.observers = combatRoomPeers(s.gameplay.rooms[key])
	s.gameplay.mu.Unlock()
	// Same live aggregate, sink and map; a fresh entry pointer is still a new
	// field visit and cannot receive an attack captured before that visit.
	s.leaveGameplay(1, 40000, 2, sinks[1])
	if err := s.enterGameplay(1, lives[1], sinks[1]); err != nil {
		t.Fatal(err)
	}
	s.publishCombatTicket(context.Background(), ticket, domain.CombatAttackResult{})
	if sinks[0].count(protocol.SendKillMonster) != 1 || sinks[1].count(protocol.SendKillMonster) != 0 {
		t.Fatal("old attack crossed an exit/reentry scope")
	}
}

func TestCombatZeroDamageDoesNotAddContributor(t *testing.T) {
	snapshot := domain.CharacterSession{CharacterID: 1, SessionID: "zero", Generation: 1}
	if len(addCombatDamage(nil, snapshot, 0)) != 0 {
		t.Fatal("zero damage consumed a historical contribution entry")
	}
}

func TestCombatDeathPublicationRetainsObserverScope(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	key := mapKey{channel: 1, mapID: 40000}
	gate := &combatProgressGateSink{entered: make(chan struct{}), release: make(chan struct{}), op: protocol.SendKillMonster}
	s.gameplay.rooms[key].members[2].sink = gate
	ticket := &combatTicket{key: key, actor: lives[0], actorSession: cloneCombatSession(lives[0].session), sink: sinks[0],
		request: domain.CombatAttackRequest{Session: lives[0].session, Targets: []domain.CombatTarget{{ObjectID: mob.objectID, MobID: mob.stats.ID, BeforeHP: mob.hp, Damage: int(mob.hp), Defeated: true}}}, targets: []*gameplayMob{mob}}
	ticket.observers = combatRoomPeers(s.gameplay.rooms[key])
	done := make(chan struct{})
	go func() {
		s.publishCombatTicket(context.Background(), ticket, domain.CombatAttackResult{})
		close(done)
	}()
	<-gate.entered
	if lives[1].persistMu.TryLock() {
		lives[1].persistMu.Unlock()
		t.Error("observer warp could overtake death publication")
	}
	close(gate.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer publication deadlocked")
	}
}

type sharedCombatRepo struct {
	domain.Repository
	parties []domain.CombatPartyPin
	apply   func(domain.CombatAttackRequest) (domain.CombatAttackResult, error)
	outcome func(string, string) (*domain.CombatAttackResult, error)
}

func (r *sharedCombatRepo) CombatParties(context.Context, domain.WorldLease, []int64) ([]domain.CombatPartyPin, error) {
	return r.parties, nil
}
func (r *sharedCombatRepo) ApplyCombatAttack(_ context.Context, q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	return r.apply(q)
}
func (r *sharedCombatRepo) CombatAttackOutcome(_ context.Context, _ domain.WorldLease, op, hash string) (*domain.CombatAttackResult, error) {
	if r.outcome != nil {
		return r.outcome(op, hash)
	}
	return nil, nil
}

func sharedWorldFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, *gameplayMob) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: g, online: map[int64]domain.CharacterSession{}}
	var lives []*liveCharacter
	var sinks []*gameplayTestSink
	for id := int64(1); id <= 2; id++ {
		live := &liveCharacter{session: domain.CharacterSession{AccountID: id, CharacterID: id, SessionID: "shared-session", Generation: 1, ChannelID: 1, Character: domain.Character{ID: id, MapID: 40000, Level: 10, Job: 100, HP: 50, MaxHP: 100, MP: 100, MaxMP: 100, STR: 50, DEX: 4, INT: 4, LUK: 4, Revision: 1, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 20}}, Skills: []domain.SkillState{{ID: 1001005, Level: 1}}, Quests: []domain.QuestState{{ID: 1035, State: 1, MobKills: map[int]int{}}}}}}
		sink := &gameplayTestSink{}
		if err = s.enterGameplay(1, live, sink); err != nil {
			t.Fatal(err)
		}
		lives = append(lives, live)
		sinks = append(sinks, sink)
		s.online[id] = cloneCombatSession(live.session)
	}
	room := g.rooms[mapKey{channel: 1, mapID: 40000}]
	var mob *gameplayMob
	for _, m := range room.mobs {
		mob = m
		break
	}
	if mob == nil {
		t.Fatal("no starter mob")
	}
	mob.hp = mob.stats.HP
	mob.life.X, mob.life.Y = 0, 0
	return s, lives, sinks, mob
}

func fakeSharedResult(q domain.CombatAttackRequest) domain.CombatAttackResult {
	result := domain.CombatAttackResult{OperationID: q.OperationID}
	result.ManifestHash, _ = domain.CombatManifestHash(q)
	for _, m := range q.Members {
		c := cloneCombatSession(m).Character
		c.Revision++
		c.EXP += 3
		if c.ID == q.Session.CharacterID && q.Family == domain.CombatPhysicalSkill {
			c.MP -= 6
		}
		result.Characters = append(result.Characters, c)
	}
	for _, t := range q.Targets {
		if t.Defeated {
			result.Spawns = append(result.Spawns, domain.CombatSpawnResult{SpawnOperationID: t.SpawnOperationID, MobID: t.MobID})
		}
	}
	return result
}

func TestSharedCombatUsesAppliedDamageAndFreezesTwoRecipients(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
	calls := 0
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if len(q.Members) != 2 || len(q.Targets) != 1 || q.Targets[0].Damage != int(mob.stats.HP-5) || len(q.Targets[0].Contributions) != 2 {
			t.Errorf("bad frozen manifest: %+v", q)
		}
		sum := int64(0)
		for _, d := range q.Targets[0].Contributions {
			sum += d.Damage
		}
		if sum != mob.stats.HP {
			t.Errorf("client overkill inflated contribution: %d", sum)
		}
		// Published quest maps must be detached from the still-live aggregate.
		lives[0].mu.Lock()
		lives[0].session.Character.Quests[0].MobKills[9300018] = 7
		lives[0].dirty = true
		lives[0].session.Character.X = 25
		lives[0].mu.Unlock()
		if q.Members[0].Character.Quests[0].MobKills[9300018] != 0 {
			t.Error("quest counter snapshot aliases mutable world map")
		}
		return fakeSharedResult(q), nil
	}
	s.repo = repo
	// A real effective five-point nonlethal hit precedes a capped final hit.
	if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 1, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 5}}}, domain.CombatNormal); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || mob.hp != mob.stats.HP-5 || len(mob.contributions) != 1 || mob.contributions[0].Damage != 5 {
		t.Fatal("nonlethal damage was not recorded atomically")
	}
	if err := s.settledMeleeGameplay(context.Background(), 1, lives[1], sinks[1], protocol.MeleeAttack{Tick: 2, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || mob.hp != 0 || mob.claiming || lives[0].session.Character.EXP != 3 || lives[1].session.Character.EXP != 3 || lives[0].session.Character.X != 25 || !lives[0].dirty {
		t.Fatal("committed party progress or movement preservation failed")
	}
	if sinks[0].count(protocol.SendKillMonster) != 1 || sinks[1].count(protocol.SendKillMonster) != 1 {
		t.Fatal("both actors did not observe one death")
	}
}

func TestCombatSnapshotOwnsQuestOrderAndCounterContainers(t *testing.T) {
	original := domain.CharacterSession{Character: domain.Character{Quests: []domain.QuestState{{ID: 1035, MobOrder: []int{9300018}, MobKills: map[int]int{9300018: 1}}}}}
	frozen := cloneCombatSession(original)
	original.Character.Quests[0].MobOrder[0] = 999
	original.Character.Quests[0].MobKills[9300018] = 10
	if frozen.Character.Quests[0].MobOrder[0] != 9300018 || frozen.Character.Quests[0].MobKills[9300018] != 1 {
		t.Fatal("combat snapshot aliases mutable quest containers")
	}
}

func TestSharedPhysicalFailureDoesNotApplyDamageContributionOrMP(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	before := mob.hp
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}, apply: func(domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		return domain.CombatAttackResult{}, domain.ErrInsufficient
	}}
	s.repo = repo
	if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Skill: 1001005, Tick: 3, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatPhysicalSkill); err != nil {
		t.Fatal(err)
	}
	if mob.hp != before || mob.claiming || len(mob.contributions) != 0 || lives[0].session.Character.MP != 100 {
		t.Fatal("failed attack applied partial cost/damage/contribution")
	}
}

func TestSharedUnknownCommitRetainsClaimUntilReceiptResolution(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	var committed domain.CombatAttackResult
	lookups := 0
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		committed = fakeSharedResult(q)
		return domain.CombatAttackResult{}, errors.New("connection lost after commit")
	}
	repo.outcome = func(_, hash string) (*domain.CombatAttackResult, error) {
		lookups++
		if lookups == 1 {
			return nil, errors.New("database temporarily unavailable")
		}
		if hash != committed.ManifestHash {
			t.Error("pending request hash changed")
		}
		return &committed, nil
	}
	s.repo = repo
	if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 4, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal); err != nil {
		t.Fatal(err)
	}
	if !mob.claiming || mob.hp == 0 || len(s.gameplay.pendingCombat) != 1 || lives[0].session.Character.EXP != 0 {
		t.Fatal("uncertain commit released spawn or guessed progress")
	}
	// Replacement connection must never receive the old generation's result.
	replacement := &liveCharacter{session: lives[1].session}
	replacement.session.Generation++
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].members[2].live = replacement
	s.resolveCombatSettlements(context.Background(), repo)
	if mob.hp != 0 || mob.claiming || len(s.gameplay.pendingCombat) != 0 || lives[0].session.Character.EXP != 3 || replacement.session.Character.EXP != 0 || lives[1].session.Character.EXP != 0 {
		t.Fatal("pending receipt did not finish once or crossed live generations")
	}
	if sinks[0].count(protocol.SendKillMonster) != 1 {
		t.Fatal("resolved death not published")
	}
}

func TestSharedTwoFinalHitsDoNotDeadlockOrDuplicateSettlement(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		close(entered)
		<-release
		return fakeSharedResult(q), nil
	}
	s.repo = repo
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.settledMeleeGameplay(context.Background(), 1, lives[1], sinks[1], protocol.MeleeAttack{Tick: 9, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal)
	}()
	<-entered
	// A second actor waits for the globally ordered persist locks, then observes
	// the reserved/dead spawn; it cannot start another settlement.
	done := make(chan struct{})
	go func() {
		_ = s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 10, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal)
		close(done)
	}()
	close(release)
	wg.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opposite actor lock order deadlocked")
	}
	if calls != 1 || mob.hp != 0 {
		t.Fatal("two actors settled one spawn twice")
	}
}
