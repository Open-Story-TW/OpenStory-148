package world

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func TestSkillPrepareBroadcastsToTheRoom(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: 4341003, Level: 30})
	body, _ := hex.DecodeString("0b3d42001ece0004")
	s.skillPrepare(1, lives[0], body)
	if sinks[1].count(protocol.SendSkillPrepare) != 1 || sinks[0].count(protocol.SendSkillPrepare) != 0 {
		t.Fatalf("prepare broadcast: peer %d self %d", sinks[1].count(protocol.SendSkillPrepare), sinks[0].count(protocol.SendSkillPrepare))
	}
	s.endSkillPrepare(1, lives[0], 4341003)
	if sinks[1].count(protocol.SendSkillPrepareCancel) != 1 {
		t.Fatal("prepare cancel not broadcast")
	}
	bad, _ := hex.DecodeString("0b3d42001dce0004") // Level 29 does not match the learned 30.
	s.skillPrepare(1, lives[0], bad)
	if sinks[1].count(protocol.SendSkillPrepare) != 1 {
		t.Fatal("mismatched level broadcast")
	}
}

const testGrenade = 9990023

func init() {
	skills.Register(skills.Handler{ID: testGrenade, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee,
		Effect: combat.Effect{SkillID: testGrenade, Damage: 100, MobCount: 3, AttackCount: 1, Time: 3 * time.Second, Box: combat.Box{LT: combat.Point{X: -100, Y: -80}, RB: combat.Point{X: 100, Y: 20}, Set: true}},
		OnHit:  []combat.MobStatusApply{{Stat: combat.MobTimeBomb, Value: 1, Explode: true, TickFrom: "damage"}}}})
}

func TestThrowGrenadeAppliesOnHitAtTheLandingPoint(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testGrenade, Level: 5})
	w := protocol.NewWriter(0)
	w.I32(int32(mob.life.X))
	w.I32(int32(mob.life.Y))
	w.I32(int32(lives[0].session.Character.Y))
	w.I32(900)
	w.I32(testGrenade)
	w.U8(1)
	w.I32(5)
	w.I32(1)
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	s.throwGrenade(context.Background(), 1, lives[0], sinks[0], w.Packet()[2:])
	s.gameplay.mu.Lock()
	has := mob.statuses.Has(combat.MobTimeBomb)
	s.gameplay.mu.Unlock()
	if !has || sinks[1].count(protocol.SendApplyMonsterStatus) == 0 {
		t.Fatal("grenade did not bomb the monster at its landing point")
	}
}

// Flashbang's throw only ends the prepare: its attack packet settles it.
func TestFlashbangThrowLeavesSettlementToItsAttack(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: 4321002, Level: 5})
	w := protocol.NewWriter(0)
	w.I32(int32(mob.life.X))
	w.I32(int32(mob.life.Y))
	w.I32(int32(lives[0].session.Character.Y))
	w.I32(800)
	w.I32(4321002)
	w.U8(1)
	w.I32(5)
	w.I32(1)
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	s.throwGrenade(context.Background(), 1, lives[0], sinks[0], w.Packet()[2:])
	if calls != 0 {
		t.Fatalf("Flashbang throw settled %d times", calls)
	}
}
