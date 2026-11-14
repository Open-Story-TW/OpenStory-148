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

const testPoisonSkill = 9990001

func init() {
	skills.Register(skills.Handler{ID: testPoisonSkill, Kind: skills.KindAttack | skills.KindMobStatus, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true, Effect: combat.Effect{
		SkillID: testPoisonSkill, Damage: 100, MobCount: 1, AttackCount: 2, MPCon: 5,
		Time: 3 * time.Second, DOT: 1, DOTTime: 3 * time.Second, DOTInterval: time.Second,
		MonsterStatus: []combat.MobStatusApply{{Stat: combat.MobPoison, Poison: true}},
	}}})
}

func genericMelee(skill int, tick uint32, oid int32, lines ...uint32) protocol.Attack {
	a := protocol.Attack{Opcode: protocol.RecvMeleeAttack, SkillID: skill, TByte: byte(1<<4 | len(lines)), Tick: tick, Speed: 6}
	t := protocol.AttackTarget{ObjectID: oid}
	for _, l := range lines {
		t.Lines = append(t.Lines, protocol.AttackLine{Raw: l, Damage: int(l)})
	}
	a.Targets = []protocol.AttackTarget{t}
	return a
}

func TestGenericAttackSettlesCostLinesAndAppliesPoison(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testPoisonSkill, Level: 1})
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], genericMelee(testPoisonSkill, 7, mob.objectID, 4, 6)); err != nil {
		t.Fatal(err)
	}
	if got.Family != domain.CombatSkill || got.Cost == nil || got.Cost.MP != 5 || got.Cost.Level != 1 || got.LinesPerTarget != 2 || len(got.Targets) != 1 || got.Targets[0].Damage != 10 || len(got.Targets[0].Lines) != 2 || got.Targets[0].Lines[1].Applied != 6 {
		t.Fatalf("generic request %+v", got)
	}
	s.gameplay.mu.Lock()
	hp, poisoned := mob.hp, mob.statuses.Has(combat.MobPoison)
	s.gameplay.mu.Unlock()
	if hp != 990 || !poisoned {
		t.Fatalf("mob hp%d poisoned=%v", hp, poisoned)
	}
	if sinks[1].count(protocol.SendMeleeAttack) != 1 || sinks[0].count(protocol.SendMeleeAttack) != 0 || sinks[1].count(protocol.SendApplyMonsterStatus) != 1 || sinks[0].count(protocol.SendApplyMonsterStatus) != 1 {
		t.Fatal("remote attack/status publication")
	}
	// DoT ticks credit the caster and never kill; expiry cancels for everyone.
	start := time.Now()
	s.gameplay.tickMobStatuses(start.Add(1100 * time.Millisecond))
	s.gameplay.mu.Lock()
	afterTick := mob.hp
	var credit int64
	for _, c := range mob.contributions {
		if c.CharacterID == 1 {
			credit = c.Damage
		}
	}
	s.gameplay.mu.Unlock()
	if afterTick >= 990 || credit != 1000-afterTick {
		t.Fatalf("poison tick hp%d credit%d", afterTick, credit)
	}
	s.gameplay.tickMobStatuses(start.Add(4 * time.Second))
	if sinks[1].count(protocol.SendCancelMonsterStatus) != 1 {
		t.Fatal("expiry not broadcast")
	}
}

func TestGenericAttackRejectsUnregisteredAndUnlearned(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	for _, a := range []protocol.Attack{genericMelee(9990999, 1, mob.objectID, 1), genericMelee(testPoisonSkill, 2, mob.objectID, 1)} {
		if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 || sinks[1].count(protocol.SendMeleeAttack) != 0 {
		t.Fatal("unregistered or unlearned skill reached settlement")
	}
}

func TestLateJoinerSeesMonsterStatus(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	now := time.Now()
	s.gameplay.mu.Lock()
	p := applyMobStatusesLocked(mob, []combat.ActiveStatus{{Stat: combat.MobStun, Value: 1, SkillID: 1111002, ExpiresAt: now.Add(5 * time.Second)}}, lives[0].session, now)
	s.gameplay.mu.Unlock()
	if p == nil {
		t.Fatal("status not applied")
	}
	s.leaveGameplay(1, 40000, 2, sinks[1])
	late := &gameplayTestSink{}
	if err := s.enterGameplay(1, lives[1], late); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pkt := range late.packets {
		r := protocol.NewReader(pkt)
		if r.U16() == protocol.SendSpawnMonster && r.I32() == mob.objectID {
			r.U8()
			r.I32()
			r.U8()
			r.Skip(28)
			found = r.U32()&(1<<uint(combat.MobStun)) != 0
		}
	}
	if !found {
		t.Fatal("late joiner spawn has no stun bit")
	}
}

const testMesoExplosion = 9990009

func init() {
	skills.Register(skills.Handler{ID: testMesoExplosion, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true, ConsumeMesos: true,
		Effect: combat.Effect{SkillID: testMesoExplosion, Damage: 100, MobCount: 6, AttackCount: 15},
		AfterHit: func(e combat.AttackEvent) {
			for _, h := range e.Hits {
				_ = e.SpawnMesos(h.X, h.Y, 7)
				_ = e.StealItem(h)
			}
		}}})
}

type mesoSkillRepo struct {
	sharedCombatRepo
	consumed [][]int32
	spawned  []domain.SkillMesoSpawnRequest
	stolen   []domain.MobStealRequest
}

func (r *mesoSkillRepo) ConsumeMesoDrops(_ context.Context, q domain.MesoConsumeRequest) ([]domain.ItemDrop, error) {
	r.consumed = append(r.consumed, q.ObjectIDs)
	var out []domain.ItemDrop
	for _, id := range q.ObjectIDs {
		out = append(out, domain.ItemDrop{ObjectID: id, Mesos: 100})
	}
	return out, nil
}
func (r *mesoSkillRepo) PeekMesoDrops(_ context.Context, q domain.MesoConsumeRequest) ([]domain.ItemDrop, error) {
	r.consumed = append(r.consumed, q.ObjectIDs)
	var out []domain.ItemDrop
	for _, id := range q.ObjectIDs {
		out = append(out, domain.ItemDrop{ObjectID: id, Mesos: 100})
	}
	return out, nil
}
func (r *mesoSkillRepo) StealMobItem(_ context.Context, q domain.MobStealRequest) (*domain.ItemDrop, error) {
	r.stolen = append(r.stolen, q)
	return &domain.ItemDrop{ObjectID: 78, InvType: 4, TemplateID: 4000000, Quantity: 1}, nil
}
func (r *mesoSkillRepo) SpawnSkillMesos(_ context.Context, q domain.SkillMesoSpawnRequest) (domain.ItemDrop, error) {
	r.spawned = append(r.spawned, q)
	return domain.ItemDrop{ObjectID: 77, Mesos: q.Amount, Quantity: 1}, nil
}

func TestMesoExplosionConsumesGroundMesosAndAfterHitSpawns(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testMesoExplosion, Level: 1})
	repo := &mesoSkillRepo{sharedCombatRepo: sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}}
	s.repo = repo
	a := genericMelee(testMesoExplosion, 4, mob.objectID, 9)
	a.MesoDrops = []int32{555, 556}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if len(repo.stolen) != 1 || repo.stolen[0].SpawnOperationID == "" || repo.stolen[0].MobID != mob.stats.ID || len(repo.consumed) != 1 || len(repo.consumed[0]) != 2 || len(repo.spawned) != 1 || repo.spawned[0].Amount != 7 || sinks[1].count(protocol.SendMeleeAttack) != 1 {
		t.Fatalf("meso explosion consumed=%v spawned=%v", repo.consumed, repo.spawned)
	}
	// Ordinary skills may not name ground mesos.
	b := genericMelee(testPoisonSkill, 5, mob.objectID, 1)
	b.MesoDrops = []int32{557}
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testPoisonSkill, Level: 1})
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].members[1].lastAttack = time.Time{}
	_ = s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], b)
	if len(repo.consumed) != 1 {
		t.Fatal("non-meso skill consumed mesos")
	}
}

func TestDamageMobsServerHitsSettleWithoutUserAttackPacket(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: 1001004, Level: 1})
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	mob.hp = 100
	if err := s.DamageMobs(context.Background(), 1, lives[0], sinks[0], 1001004, []combat.ServerHit{{ObjectID: mob.objectID, Damage: 40}}); err != nil {
		t.Fatal(err)
	}
	if mob.hp != 60 || sinks[1].count(protocol.SendDamageMonster) != 1 || sinks[1].count(protocol.SendMeleeAttack) != 0 {
		t.Fatalf("server hit hp%d", mob.hp)
	}
	if err := s.DamageMobs(context.Background(), 1, lives[0], sinks[0], 1001004, []combat.ServerHit{{ObjectID: mob.objectID, Damage: 999}}); err != nil {
		t.Fatal(err)
	}
	if got.Family != domain.CombatSkill || len(got.Targets) != 1 || !got.Targets[0].Defeated || got.Targets[0].Damage != 60 {
		t.Fatalf("lethal server hit %+v", got)
	}
	if err := s.DamageMobs(context.Background(), 1, lives[1], sinks[1], 1001004, []combat.ServerHit{{ObjectID: mob.objectID, Damage: 1}}); err != nil {
		t.Fatal(err)
	}
}

const testReviveTrigger = 9990011

func init() {
	skills.Register(skills.Handler{ID: testReviveTrigger, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Body, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testReviveTrigger, Damage: 100, MobCount: 1, AttackCount: 1},
		AfterHit: func(e combat.AttackEvent) {
			if e.SummonCount(testSummonSkill) < 4 {
				_ = e.SpawnSummon(testSummonSkill, 2)
			}
			_ = e.GainForce(e.Targets[0], 3, 4)
		}}})
}

func TestBodyAttackRoutesAndAfterHitSpawnsSummonAndForce(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testReviveTrigger, Level: 1}, domain.SkillState{ID: testSummonSkill, Level: 2})
	a := genericMelee(testReviveTrigger, 21, mob.objectID, 5)
	a.Opcode = protocol.RecvBodyAttack
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendBodyAttack) != 1 || sinks[1].count(protocol.SendSpawnSummon) != 1 || sinks[0].count(protocol.SendGainForce) != 1 {
		t.Fatal("body attack / trigger summon / gain force")
	}
}

const testLinkedHidden = 9990012

func init() {
	skills.Register(skills.Handler{ID: testLinkedHidden, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true, LevelFrom: testPoisonSkill,
		Effect: combat.Effect{SkillID: testLinkedHidden, Damage: 100, MobCount: 1, AttackCount: 1}}})
}

func TestLinkedHiddenAttackUsesTheLearnedSkillLevel(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	a := genericMelee(testLinkedHidden, 31, mob.objectID, 5)
	_ = s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a)
	if sinks[1].count(protocol.SendMeleeAttack) != 0 {
		t.Fatal("hidden attack accepted without the main skill")
	}
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testPoisonSkill, Level: 3})
	a.Tick = 32
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].members[1].lastAttack = time.Time{}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendMeleeAttack) != 1 {
		t.Fatal("linked hidden attack rejected")
	}
	_ = got
}
