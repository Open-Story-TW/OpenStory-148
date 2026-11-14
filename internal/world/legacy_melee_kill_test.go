package world

import (
	"context"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestLegacyMeleeNativeSnailKillRetainsRawBoundedEvidence(t *testing.T) {
	s, lives, sinks, template := sharedWorldFixture(t)
	live, sink := lives[0], sinks[0]
	c := &live.session.Character
	c.Job, c.STR, c.DEX, c.INT, c.LUK, c.X, c.Y = 0, 300, 6, 4, 4, -15, 215
	c.Equipment = []domain.Equipment{{ItemID: 1302000, Slot: -11, WATK: 17}}
	s.online[c.ID] = live.session
	wire, _ := hex.DecodeString("4b00011100000000000000000000070051955cc9010459ec5c1e0000000043420f00070100011e00d7001e00d7008901a5000000458835b42667347ef1ffd700")
	wire = append(wire[:2:2], protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, wire[2:])...) // archived v145 frame
	attack, err := protocol.ReadMeleeAttack(protocol.NewReader(wire[2:]))
	if err != nil || len(attack.Targets) != 1 || attack.Targets[0].Damage != 165 {
		t.Fatal(attack, err)
	}
	mob := *template
	mob.objectID = attack.Targets[0].ObjectID
	mob.stats = content.Mob{ID: 100101, Level: 2, HP: 20, EXP: 3}
	mob.hp, mob.life.X, mob.life.Y, mob.contributions = 20, 30, 215, nil
	s.gameplay.rooms[mapKey{1, 40000}].mobs[mob.objectID] = &mob
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if q.Family != domain.CombatNormal || q.SkillID != 0 || len(q.Targets) != 1 || q.Targets[0].BeforeHP != 20 || q.Targets[0].Damage != 20 || !q.Targets[0].Defeated || len(q.Targets[0].Lines) != 1 || q.Targets[0].Lines[0].Raw != 165 || q.Targets[0].Lines[0].Applied != 20 || q.Targets[0].Lines[0].Criticality != "" {
			t.Fatal("world lost original165/bounded20", q)
		}
		return fakeSharedResult(q), nil
	}}
	if err := s.settledMeleeGameplay(context.Background(), 1, live, sink, attack, domain.CombatNormal); err != nil || calls != 1 || mob.hp != 0 || len(s.gameplay.pendingCombat) != 0 {
		t.Fatal("normal kill did not settle", calls, mob.hp, err)
	}
}
