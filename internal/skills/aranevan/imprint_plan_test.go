package aranevan

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// A critical line on an imprinted monster keeps the critical envelope over
// the imprinted normal bound (live: 4067 was clipped to 2959).
func TestPhantomImprintKeepsTheCriticalEnvelope(t *testing.T) {
	c := domain.Character{ID: 9, Job: 100, Level: 30, HP: 500, MP: 100, STR: 100, DEX: 30,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 40}}}
	s, _ := combat.BaseStats(c, time.Now())
	e := combat.Effect{SkillID: 1001005, Level: 1, Damage: 130, MobCount: 8, AttackCount: 1,
		Box: combat.Box{LT: combat.Point{X: -150, Y: -60}, RB: combat.Point{X: 0, Y: 10}, Set: true}}
	mob := content.Mob{HP: 1_000_000}
	imprinted := combat.MobStatuses{combat.MobWeakness: {Stat: combat.MobWeakness, Value: 20, SkillID: 22161002, ExpiresAt: time.Unix(200, 0)}}
	raw := uint32(0x80000000 | 999999)
	a := protocol.Attack{Opcode: protocol.RecvMeleeAttack, SkillID: 1001005, Display: 0x0020, TByte: 0x11,
		Targets: []protocol.AttackTarget{{ObjectID: 5, Lines: []protocol.AttackLine{{Raw: raw, Damage: int(raw & 0x7fffffff)}}}}}
	ctx := combat.AttackContext{Kind: combat.Melee, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0), Attack: a,
		Targets: []combat.TargetInfo{{ObjectID: 5, Mob: mob, X: -100, HP: 1_000_000, Statuses: imprinted}}}
	dc := combat.DamageContext{Stats: s, Effect: e, Mob: mob, Attacker: c, Statuses: imprinted}
	normal := combat.LineCeiling(dc) * 120 / 100
	p, err := combat.PlanAttack(&combat.AttackSpec{Kind: combat.Melee}, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Targets[0].Lines[0], combat.CritCeiling(dc, normal); got != want || got <= normal {
		t.Fatalf("imprinted critical line %d, want %d over the imprinted normal %d", got, want, normal)
	}
}
