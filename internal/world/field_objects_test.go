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
	testMistSkill = 9990004
	testDoorSkill = 9990005
	testAuraSkill = 9990022
)

func init() {
	skills.Register(skills.Handler{ID: testMistSkill, Kind: skills.KindFieldObject, Attack: &combat.AttackSpec{Kind: combat.Magic,
		Effect: combat.Effect{SkillID: testMistSkill, Time: 6 * time.Second, DOT: 1, DOTTime: 3 * time.Second},
		Field: &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaPoison, Box: combat.Box{LT: combat.Point{X: -100, Y: -50}, RB: combat.Point{X: 100, Y: 20}, Set: true},
			MobStatus: []combat.MobStatusApply{{Stat: combat.MobPoison, Poison: true}}}}})
	skills.Register(skills.Handler{ID: testAuraSkill, Kind: skills.KindFieldObject, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testAuraSkill, Time: 6 * time.Second, X: 10},
		Field: &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaRecovery, Box: combat.Box{LT: combat.Point{X: -100, Y: -50}, RB: combat.Point{X: 100, Y: 20}, Set: true}, RecoverMP: true}}})
	skills.Register(skills.Handler{ID: testDoorSkill, Kind: skills.KindFieldObject, Attack: &combat.AttackSpec{Kind: combat.Magic, Effect: combat.Effect{SkillID: testDoorSkill, Time: 30 * time.Second},
		Field: &combat.FieldSpec{Kind: combat.FieldTownPortal}}})
}

func TestPoisonAreaTicksMobsInsideAndExpires(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	now := time.Now()
	if _, err := s.SpawnFieldObject(1, lives[0], testMistSkill, 1, now); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendAffectedAreaCreated) != 1 {
		t.Fatal("area not broadcast")
	}
	s.gameplay.mu.Lock()
	var outside *gameplayMob
	for _, m := range s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].mobs {
		if m != mob {
			outside = m
			m.life.X = 5000
		}
	}
	s.gameplay.mu.Unlock()
	s.gameplay.tickAreas(now.Add(time.Millisecond))
	s.gameplay.mu.Lock()
	in := mob.statuses.Has(combat.MobPoison)
	out := outside != nil && outside.statuses.Has(combat.MobPoison)
	s.gameplay.mu.Unlock()
	if !in || out {
		t.Fatalf("poison inside=%v outside=%v", in, out)
	}
	late := &gameplayTestSink{}
	s.leaveGameplay(1, 40000, 2, sinks[1])
	if err := s.enterGameplay(1, lives[1], late); err != nil {
		t.Fatal(err)
	}
	if late.count(protocol.SendAffectedAreaCreated) != 1 {
		t.Fatal("late joiner missing area")
	}
	s.gameplay.tickAreas(now.Add(7 * time.Second))
	if late.count(protocol.SendAffectedAreaRemoved) != 1 {
		t.Fatal("area did not expire")
	}
}

func TestTownPortalVisibleToOwnerOnlyWithoutParty(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	// 50000 returns to the town 1000000, which has a "tp" portal.
	for i, live := range lives {
		s.leaveGameplay(1, 40000, int64(i+1), sinks[i])
		live.session.Character.MapID = 50000
		if err := s.enterGameplay(1, live, sinks[i]); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if _, err = s.SpawnFieldObject(1, lives[0], testDoorSkill, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sinks[0].count(protocol.SendTownPortalCreated) != 1 || sinks[0].count(protocol.SendTownPortalInfo) != 1 || sinks[1].count(protocol.SendTownPortalCreated) != 0 {
		t.Fatal("door visibility: owner only without a party")
	}
	// A non-party member cannot use someone else's door.
	if err = s.useTownPortal(context.Background(), 1, lives[1], sinks[1], protocol.UseTownPortal{OwnerID: 1, ToTown: true}); err != nil {
		t.Fatal(err)
	}
	if lives[1].session.Character.MapID != 50000 || sinks[1].count(protocol.SendUpdateStats) == 0 {
		t.Fatal("stranger used a private door")
	}
	s.RemoveTownPortal(1, 1)
	if sinks[0].count(protocol.SendTownPortalRemoved) != 1 {
		t.Fatal("door removal")
	}
}

const (
	testThreatSkill = 9990006
	testInferno     = 9990007
)

func init() {
	skills.Register(skills.Handler{ID: testThreatSkill, Kind: skills.KindMobStatus, Attack: &combat.AttackSpec{Kind: combat.Magic,
		Effect:  combat.Effect{SkillID: testThreatSkill, MobCount: 2, Time: 5 * time.Second, X: -20},
		MobCast: &combat.MobCastSpec{Dispel: true, Statuses: []combat.MobStatusApply{{Stat: combat.MobPDR, ValueFrom: "x"}}}}})
	skills.Register(skills.Handler{ID: testInferno, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Magic, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testInferno, Damage: 100, MobCount: 1, AttackCount: 1}, EruptAreas: []int{testMistSkill}}})
}

func TestCastOnMobsDispelsAndDebuffs(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	now := time.Now()
	s.gameplay.mu.Lock()
	mob.statuses = combat.MobStatuses{combat.MobPowerUp: {Stat: combat.MobPowerUp, Value: 10, ExpiresAt: now.Add(time.Minute)}}
	s.gameplay.mu.Unlock()
	n, err := s.CastOnMobs(1, lives[0], testThreatSkill, 1, []int32{mob.objectID, 424242}, now)
	if err != nil || n != 1 {
		t.Fatalf("cast %d %v", n, err)
	}
	s.gameplay.mu.Lock()
	powered, pdr := mob.statuses.Has(combat.MobPowerUp), mob.statuses[combat.MobPDR]
	s.gameplay.mu.Unlock()
	if powered || pdr == nil || pdr.Value != -20 || sinks[1].count(protocol.SendCancelMonsterStatus) != 1 || sinks[1].count(protocol.SendApplyMonsterStatus) != 1 {
		t.Fatal("dispel/debuff")
	}
}

func TestAttackEruptsOwnMist(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testInferno, Level: 1})
	if _, err := s.SpawnFieldObject(1, lives[0], testMistSkill, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	a := genericMelee(testInferno, 3, mob.objectID, 5)
	a.Opcode = protocol.RecvMagicAttack
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendAffectedAreaRemoved) != 1 {
		t.Fatal("mist not erupted")
	}
}

const testMistAttack = 9990010

func init() {
	skills.Register(skills.Handler{ID: testMistAttack, Kind: skills.KindAttack | skills.KindFieldObject, Attack: &combat.AttackSpec{Kind: combat.Magic, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testMistAttack, Damage: 100, MobCount: 1, AttackCount: 1, Time: 6 * time.Second},
		Field:  &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaPoison, Box: combat.Box{LT: combat.Point{X: -50, Y: -50}, RB: combat.Point{X: 50, Y: 10}, Set: true}}}})
}

func TestMagicAttackPlacesItsFieldObject(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testMistAttack, Level: 1})
	a := protocol.Attack{Opcode: protocol.RecvMagicAttack, SkillID: testMistAttack, TByte: 0x01, Tick: 9}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendAffectedAreaCreated) != 1 {
		t.Fatal("mist attack did not place its area")
	}
}

func TestRecoveryAuraRestoresMPInside(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	for i, x := range []int{0, 5000} {
		c := &lives[i].session.Character
		c.X, c.Y, c.MP, c.MaxMP = x, 0, 100, 1000
	}
	now := time.Now()
	if _, err := s.SpawnFieldObject(1, lives[0], testAuraSkill, 1, now); err != nil {
		t.Fatal(err)
	}
	s.gameplay.tickAreas(now.Add(time.Millisecond))
	if mp := lives[0].session.Character.MP; mp != 200 || sinks[0].count(protocol.SendUpdateStats) == 0 {
		t.Fatalf("inside MP %d", mp)
	}
	if mp := lives[1].session.Character.MP; mp != 100 {
		t.Fatalf("outside MP %d", mp)
	}
	s.gameplay.tickAreas(now.Add(500 * time.Millisecond)) // Before the next interval.
	if mp := lives[0].session.Character.MP; mp != 200 {
		t.Fatalf("ticked early: %d", mp)
	}
}
