package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

const (
	testSummonSkill = 9990002
	testPuppetSkill = 9990003
)

func init() {
	skills.Register(skills.Handler{ID: testSummonSkill, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testSummonSkill, Damage: 100, MobCount: 3, AttackCount: 1},
		Summon: &combat.SummonSpec{MoveAbility: 4, AssistType: 1, Duration: 10 * time.Second}}})
	skills.Register(skills.Handler{ID: testPuppetSkill, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testPuppetSkill},
		Summon: &combat.SummonSpec{AssistType: 0, Puppet: true, HP: 50, Duration: 10 * time.Second}}})
}

func summonFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, *gameplayMob, int32) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testSummonSkill, Level: 2}, domain.SkillState{ID: testPuppetSkill, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s, lives, sinks, mob, oid
}

func TestSummonSpawnLateJoinMoveAndOwnerLeave(t *testing.T) {
	s, lives, sinks, _, oid := summonFixture(t)
	if sinks[0].count(protocol.SendSpawnSummon) != 1 || sinks[1].count(protocol.SendSpawnSummon) != 1 {
		t.Fatal("spawn not broadcast to owner and peer")
	}
	// Recast replaces the previous same-skill summon.
	if _, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("recast did not replace summon")
	}
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}]
	if len(room.summons) != 1 || room.summons[oid] != nil {
		t.Fatal("old summon survived recast")
	}
	for id := range room.summons {
		oid = id
	}
	s.gameplay.mu.Unlock()
	s.summonMove(1, lives[0], protocol.SummonMoveInput{ObjectID: oid, X: 30, Y: -5, Changed: true, Fragments: []byte{0}})
	s.summonMove(1, lives[1], protocol.SummonMoveInput{ObjectID: oid, X: 999, Changed: true, Fragments: []byte{0}})
	s.gameplay.mu.Lock()
	x := room.summons[oid].x
	s.gameplay.mu.Unlock()
	if x != 30 || sinks[1].count(protocol.SendMoveSummon) != 1 {
		t.Fatalf("summon move x%d (non-owner must not move it)", x)
	}
	s.leaveGameplay(1, 40000, 2, sinks[1])
	late := &gameplayTestSink{}
	if err := s.enterGameplay(1, lives[1], late); err != nil {
		t.Fatal(err)
	}
	if late.count(protocol.SendSpawnSummon) != 1 {
		t.Fatal("late joiner missing summon")
	}
	s.leaveGameplay(1, 40000, 1, sinks[0])
	if late.count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("owner leave did not remove summon")
	}
}

func TestSummonAttackSettlesWithoutCostAndExpires(t *testing.T) {
	s, lives, sinks, mob, oid := summonFixture(t)
	mob.hp = 1000
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 5, Action: 0x80 | 3, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 7, Damage: 7}}}}}
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	hp := mob.hp
	s.gameplay.mu.Unlock()
	if hp != 993 || calls != 0 || sinks[1].count(protocol.SendSummonAttack) != 1 || sinks[0].count(protocol.SendSummonAttack) != 0 {
		t.Fatalf("nonlethal summon attack hp%d calls%d", hp, calls)
	}
	// A peer cannot drive another player's summon.
	if err := s.summonAttackGameplay(context.Background(), 1, lives[1], sinks[1], protocol.SummonAttackInput{ObjectID: oid, Tick: 9, Targets: in.Targets}); err != nil {
		t.Fatal(err)
	}
	if sinks[0].count(protocol.SendSummonAttack) != 0 {
		t.Fatal("foreign summon control accepted")
	}
	// Lethal attack goes through settlement with a zero-cost summon request.
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	s.gameplay.mu.Lock()
	mob.hp = 5
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[oid].lastAttack = time.Now().Add(-time.Second) // past the 200ms throttle
	s.gameplay.mu.Unlock()
	in.Tick = 77
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if got.Cost == nil || got.Cost.Summon != oid || got.Cost.MP != 0 || len(got.Targets) != 1 || !got.Targets[0].Defeated || got.Targets[0].Damage != 5 {
		t.Fatalf("summon settlement %+v", got)
	}
	s.gameplay.tickSummons(time.Now().Add(11 * time.Second))
	if sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("summon did not expire")
	}
}

func TestPuppetAbsorbsDamageAndDies(t *testing.T) {
	s, lives, sinks, _, _ := summonFixture(t)
	oid, err := s.SpawnSummon(1, lives[0], testPuppetSkill, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.summonDamaged(1, lives[0], protocol.SummonDamageInput{ObjectID: oid, AttackIndex: -1, Damage: 30})
	s.summonDamaged(1, lives[0], protocol.SummonDamageInput{ObjectID: oid, AttackIndex: 0, Damage: 30, MobTemplate: 100100})
	if sinks[1].count(protocol.SendDamageSummon) != 2 || sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("puppet damage/death")
	}
}

const testBeholder = 9990008

func init() {
	skills.Register(skills.Handler{ID: testBeholder, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testBeholder},
		Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 2, Duration: time.Minute, Sub: func(c combat.SubSummonContext) (combat.SubSummonResult, error) {
			if c.SubSkill != 1001004 {
				return combat.SubSummonResult{}, combat.ErrRejected
			}
			return combat.SubSummonResult{HealHP: 25, Action: 5}, nil
		}}}})
}

func TestBeholderSubSkillHealsOwnerAndBroadcasts(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testBeholder, Level: 1}, domain.SkillState{ID: 1001004, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testBeholder, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: oid, SkillID: 1001004}); err != nil {
		t.Fatal(err)
	}
	if lives[0].session.Character.HP != 75 || sinks[1].count(protocol.SendSummonSkill) != 1 {
		t.Fatalf("beholder heal hp%d", lives[0].session.Character.HP)
	}
	// Peers cannot trigger another player's summon skill.
	_ = s.summonSub(1, lives[1], sinks[1], protocol.SubSummonInput{ObjectID: oid, SkillID: 1001004})
	if sinks[0].count(protocol.SendSummonSkill) != 1 {
		t.Fatal("foreign sub-summon accepted")
	}
}

func TestMalformedSummonPacketsDoNotPoisonTheConnectionReader(t *testing.T) {
	s, lives, sinks, _, _ := summonFixture(t)
	for _, op := range []uint16{protocol.RecvMoveSummon, protocol.RecvSummonAttack, protocol.RecvDamageSummon, protocol.RecvSubSummon, protocol.RecvRemoveSummon} {
		r := protocol.NewReader([]byte{1, 2, 3})
		handled, err := s.handleGameplay(context.Background(), 1, lives[0], sinks[0], op, r)
		if !handled || err != nil || r.Err() != nil {
			t.Fatalf("op 0x%x: handled=%v err=%v readerErr=%v", op, handled, err, r.Err())
		}
	}
}

const (
	testFactory    = 9990013
	testFactoryBot = 9990014
	testMine       = 9990015
)

func init() {
	skills.Register(skills.Handler{ID: testFactoryBot, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testFactoryBot},
		Summon: &combat.SummonSpec{MoveAbility: 2, AssistType: 1, Multi: true, Duration: 5 * time.Second}}})
	skills.Register(skills.Handler{ID: testFactory, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testFactory},
		Summon: &combat.SummonSpec{AssistType: 5, Duration: time.Minute, Sub: func(combat.SubSummonContext) (combat.SubSummonResult, error) {
			return combat.SubSummonResult{Spawn: []combat.SummonChild{{SkillID: testFactoryBot, Level: 1}, {SkillID: testFactoryBot, Level: 1}, {SkillID: testFactoryBot, Level: 1}}}, nil
		}}}})
	skills.Register(skills.Handler{ID: testMine, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true, Effect: combat.Effect{SkillID: testMine, Damage: 100, MobCount: 3, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 0, SingleAttack: true, Multi: true, Duration: 2 * time.Second}}})
}

func TestFactorySubSpawnsChildrenAndMinesAttackOnce(t *testing.T) {
	s, lives, sinks, mob, _ := summonFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testFactory, Level: 1}, domain.SkillState{ID: testFactoryBot, Level: 1}, domain.SkillState{ID: testMine, Level: 1})
	fid, err := s.SpawnSummon(1, lives[0], testFactory, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := sinks[1].count(protocol.SendSpawnSummon)
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: fid}); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendSpawnSummon) != before+3 {
		t.Fatal("factory did not spawn three robots")
	}
	mid, err := s.SpawnSummon(1, lives[0], testMine, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	in := protocol.SummonAttackInput{ObjectID: mid, Tick: 3, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 9, Damage: 9}}}}}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	_, still := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[mid]
	s.gameplay.mu.Unlock()
	if mob.hp != 991 || still {
		t.Fatalf("mine attack hp%d still=%v", mob.hp, still)
	}
}

const (
	testKnights    = 9990016
	testKnightsAlt = 9990017
	testRemapFrom  = 9990018
	testRemapTo    = 9990019
	testNoCast     = 9990020
	testBlink      = 9990021
)

var seenSummonSkill int

func init() {
	skills.Register(skills.Handler{ID: testKnights, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true, Prepare: func(c *combat.AttackContext) error { seenSummonSkill = c.SummonSkillID; return nil },
		Effect: combat.Effect{SkillID: testKnights, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 1, Duration: time.Minute, Variants: []int{testKnightsAlt}}}})
	skills.Register(skills.Handler{ID: testNoCast, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testNoCast, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 1, Duration: time.Minute, NoCastSpawn: true}}})
	skills.Register(skills.Handler{ID: testBlink, Kind: skills.KindBuff, Attack: &combat.AttackSpec{TeleportKey: true}})
	skills.Register(skills.Handler{ID: testRemapTo, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true, NoCooldown: true, Effect: combat.Effect{SkillID: testRemapTo, Damage: 100, MobCount: 1, AttackCount: 1, Cooltime: time.Minute}}})
	skills.Register(skills.Handler{ID: testRemapFrom, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testRemapFrom, Damage: 100, MobCount: 1, AttackCount: 1},
		Remap:  func(domain.Character, time.Time) int { return testRemapTo }}})
}

func TestSummonVariantsAndAttackRemap(t *testing.T) {
	s, lives, sinks, mob, _ := summonFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testKnights, Level: 1}, domain.SkillState{ID: testRemapFrom, Level: 1}, domain.SkillState{ID: testRemapTo, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testKnights, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	shown := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[oid].skillID
	s.gameplay.mu.Unlock()
	if shown != testKnightsAlt {
		t.Fatalf("variant not shown: %d", shown)
	}
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 4, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 3, Damage: 3}}}}}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil || mob.hp != 997 {
		t.Fatalf("variant summon attack hp%d %v", mob.hp, err)
	}
	if seenSummonSkill != testKnightsAlt {
		t.Fatalf("attack context summon skill %d", seenSummonSkill)
	}
	s.castCombatObjects(lives[0].session, lives[0], sinks[0], protocol.SkillCast{SkillID: testNoCast, Level: 1})
	if n := s.summonCount(mapKey{channel: 1, mapID: 40000}, lives[0].session.CharacterID, testNoCast); n != 0 {
		t.Fatalf("NoCastSpawn cast spawned a summon: %d", n)
	}
	s.castCombatObjects(lives[0].session, lives[0], sinks[0], protocol.SkillCast{SkillID: testBlink, Level: 1})
	if sinks[0].count(protocol.SendRandomTeleportKey) != 1 {
		t.Fatal("Dragon Blink cast did not re-roll the teleport key")
	}
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	mob.hp = 2
	if err = s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], genericMelee(testRemapFrom, 41, mob.objectID, 5)); err != nil {
		t.Fatal(err)
	}
	if got.SkillID != testRemapTo || got.Cost == nil || got.Cost.CooldownMS != 0 {
		t.Fatalf("remap/no-cooldown %+v", got)
	}
}
