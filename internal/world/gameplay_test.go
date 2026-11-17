package world

import (
	"context"
	"encoding/binary"
	"maple-go-server/internal/content"
	"sync"
	"sync/atomic"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type gameplayTestSink struct {
	mu      sync.Mutex
	packets [][]byte
}

func (s *gameplayTestSink) WritePacket(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, append([]byte(nil), p...))
	return nil
}
func (s *gameplayTestSink) count(op uint16) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.packets {
		if len(p) >= 2 && binary.LittleEndian.Uint16(p) == op {
			n++
		}
	}
	return n
}

func TestMonsterControlPriorityLeavesExclusiveRequestCooldownAlone(t *testing.T) {
	s := &Server{gameplay: &gameplaySpace{}}
	sink := &gameplayTestSink{}
	packet := protocol.NewWriter(protocol.RecvMonsterControlPriority)
	packet.I32(1000152)
	packet.U32(660)
	handled, err := s.handleGameplay(context.Background(), 0, &liveCharacter{}, sink,
		protocol.RecvMonsterControlPriority, protocol.NewReader(packet.Packet()[2:]))
	if err != nil || !handled || len(sink.packets) != 0 {
		t.Fatalf("periodic mob report must be handled without a reply: handled=%t, err=%v, packets=%d", handled, err, len(sink.packets))
	}
}

type gameplayTestRepo struct {
	domain.Repository
	reward func(domain.CharacterSession, string, int64) (domain.Character, error)
	save   func(domain.CharacterSession, domain.Character) error
}

func (r gameplayTestRepo) AwardCombat(_ context.Context, s domain.CharacterSession, op string, xp int64) (domain.Character, error) {
	return r.reward(s, op, xp)
}
func (r gameplayTestRepo) CombatParties(context.Context, domain.WorldLease, []int64) ([]domain.CombatPartyPin, error) {
	return nil, nil
}
func (r gameplayTestRepo) CombatAttackOutcome(context.Context, domain.WorldLease, string, string) (*domain.CombatAttackResult, error) {
	return nil, nil
}
func (r gameplayTestRepo) ApplyCombatAttack(_ context.Context, q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	if r.reward == nil {
		return domain.CombatAttackResult{}, domain.ErrForbidden
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return domain.CombatAttackResult{}, err
	}
	var xp int64
	op := q.OperationID
	for _, t := range q.Targets {
		if t.Defeated {
			mob, e := catalog.Mob(t.MobID)
			if e != nil {
				return domain.CombatAttackResult{}, e
			}
			xp += mob.EXP
			op = t.SpawnOperationID
		}
	}
	c, err := r.reward(q.Session, op, xp)
	if err != nil {
		return domain.CombatAttackResult{}, err
	}
	return domain.CombatAttackResult{Characters: []domain.Character{c}}, nil
}
func (r gameplayTestRepo) SaveCharacter(_ context.Context, s domain.CharacterSession, c domain.Character) error {
	return r.save(s, c)
}

func TestGameplayTwoPlayersKillOneMobOnlyOnce(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var rewards atomic.Int32
	repo := gameplayTestRepo{reward: func(s domain.CharacterSession, op string, xp int64) (domain.Character, error) {
		rewards.Add(1)
		close(entered)
		<-release
		c := s.Character
		c.EXP += xp
		c.Revision++
		return c, nil
	}}
	s := &Server{gameplay: g, repo: repo, online: make(map[int64]domain.CharacterSession)}
	players := []*liveCharacter{
		{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, SessionID: "test-gameplay-one", Generation: 1, Character: domain.Character{ID: 1, MapID: 40000, Job: 0, Level: 1, HP: 50, MaxHP: 50, STR: 100, DEX: 10, Revision: 1, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11, WATK: 100}}}}},
		{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, SessionID: "test-gameplay-two", Generation: 1, Character: domain.Character{ID: 2, MapID: 40000, Job: 0, Level: 1, HP: 50, MaxHP: 50, STR: 100, DEX: 10, Revision: 1, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11, WATK: 100}}}}},
	}
	sinks := []*gameplayTestSink{{}, {}}
	for i, p := range players {
		if err = s.enterGameplay(1, p, sinks[i]); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	room := g.rooms[mapKey{channel: 1, mapID: 40000}]
	var target *gameplayMob
	for _, m := range room.mobs {
		target = m
		break
	}
	g.mu.Unlock()
	for _, p := range players {
		p.session.Character.X = target.life.X
		p.session.Character.Y = target.life.Y
	}
	a := protocol.MeleeAttack{Tick: 1, Display: 35, Speed: 6, Targets: []protocol.MeleeTarget{{ObjectID: target.objectID, Damage: 10000}}}
	done := make(chan error, 1)
	go func() { done <- s.meleeGameplay(context.Background(), 1, players[0], sinks[0], a) }()
	<-entered
	// A second character hitting the same dying spawn must not receive a reward.
	if err = s.meleeGameplay(context.Background(), 1, players[1], sinks[1], a); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if rewards.Load() != 1 {
		t.Fatalf("reward operations=%d", rewards.Load())
	}
	for i, sink := range sinks {
		if sink.count(protocol.SendKillMonster) != 1 {
			t.Fatalf("player%d did not observe one death", i+1)
		}
	}
	if players[1].session.Character.EXP != 0 {
		t.Fatal("competing player received duplicate reward")
	}
	// The same client tick is ignored even after the first reward finishes.
	if err = s.meleeGameplay(context.Background(), 1, players[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if rewards.Load() != 1 {
		t.Fatal("replayed attack was rewarded")
	}
}

func TestPortalRequiresLocalWZPortalAndPersistsBeforeTransition(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	var saved []domain.Character
	s := &Server{gameplay: g, maps: newMapSpace(800), drops: newDropSpace(), online: make(map[int64]domain.CharacterSession), repo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error { saved = append(saved, c); return nil }}}
	p := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 10000, HP: 50, Revision: 1}}}
	sink := &gameplayTestSink{}
	seedBoundScene(t, s, p, sink)
	if err = s.enterGameplay(1, p, sink); err != nil {
		t.Fatal(err)
	}
	req := protocol.PortalRequest{TargetMap: -1, Name: "out00"}
	if err = s.portalGameplay(context.Background(), 1, p, sink, req); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatal("far-away portal accepted")
	}
	p.session.Character.X = 1077
	p.session.Character.Y = 480
	if err = s.portalGameplay(context.Background(), 1, p, sink, req); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].MapID != 20000 || p.session.Character.MapID != 20000 || p.session.Character.Revision != 2 {
		t.Fatalf("transition not persisted: %+v", saved)
	}
	if sink.count(protocol.SendSetField) != 1 {
		t.Fatal("missing local map warp packet")
	}
}
