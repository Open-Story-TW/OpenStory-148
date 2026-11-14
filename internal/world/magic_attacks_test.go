package world

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func worldMagic(t *testing.T, oid int32, raw uint32, x, y int16) protocol.MagicAttack {
	t.Helper()
	w := protocol.NewRawWriter()
	w.U8(2)
	w.U8(0x11)
	w.I32(1000)
	w.Zero(4)
	w.U8(0)
	w.U8(0)
	w.U16(44)
	w.Zero(5)
	w.U8(6)
	w.U32(99)
	w.Zero(4)
	w.I32(oid)
	w.Zero(18) // v148 target metadata
	w.U32(raw)
	w.Zero(8)
	w.I16(x)
	w.I16(y)
	w.U8(0)
	a, err := protocol.ReadBeginnerMagicAttack(protocol.NewReader(w.Packet()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNativeMagicUsesFullSharedLedgerAndForeignSerializer(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Job = 0
	lives[0].session.Character.Skills = []domain.SkillState{{ID: 1000, Level: 1}}
	mob.life.X = 80
	mob.hp = 5
	mob.contributions = []domain.CombatContribution{{CharacterID: 2, SessionID: lives[1].session.SessionID, Generation: 1, Damage: mob.stats.HP - 5}}
	calls := 0
	s.repo = &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}, apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if q.Family != domain.CombatThreeSnails || q.LinesPerTarget != 1 || len(q.Wire) != 67 || len(q.Members) != 2 || q.Targets[0].Damage != 5 || q.Targets[0].Lines[0].Raw != 10 || q.Targets[0].Lines[0].Applied != 5 || len(q.Targets[0].Contributions) != 2 {
			t.Fatalf("magic bypassed full shared manifest: %+v", q)
		}
		r := fakeSharedResult(q)
		for i := range r.Characters {
			if r.Characters[i].ID == 1 {
				r.Characters[i].MP -= 3
			}
		}
		return r, nil
	}}
	a := worldMagic(t, mob.objectID, 10, 0, 0)
	if err := s.magicGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || mob.hp != 0 || lives[0].session.Character.MP != 97 || lives[1].session.Character.EXP != 3 || sinks[0].count(0x136) != 0 || sinks[1].count(0x136) != 1 || sinks[0].count(protocol.SendKillMonster) != 1 {
		t.Fatal("magic cost/reward/local/foreign split failed")
	}
	for _, packet := range buffPackets(sinks[1]) {
		if len(packet) >= 2 && binary.LittleEndian.Uint16(packet) == 0x136 && (len(packet) != 31 || binary.LittleEndian.Uint32(packet[27:]) != 5) {
			t.Fatal("magic foreign line was not authoritative applied damage")
		}
	}
}

func TestMagicRejectsCriticalOverdamageRangeFacingAndRollback(t *testing.T) {
	for _, kind := range []string{"critical", "overdamage", "range", "facing", "unlearned", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			lives[0].session.Character.Job = 0
			lives[0].session.Character.Skills = []domain.SkillState{{ID: 1000, Level: 1}}
			mob.life.X = 80
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return domain.CombatAttackResult{}, domain.ErrInsufficient
			}}
			a := worldMagic(t, mob.objectID, 10, 0, 0)
			switch kind {
			case "critical":
				a.Targets[0].Lines[0].Raw = 0x8000000a
			case "overdamage":
				a.Targets[0].Lines[0].Raw = 11
			case "range":
				mob.life.X = 1000
			case "facing":
				mob.life.X = -20
			case "unlearned":
				lives[0].session.Character.Skills = nil
			}
			before := mob.hp
			if err := s.magicGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
				t.Fatal(err)
			}
			if mob.hp != before || mob.claiming || len(mob.contributions) != 0 || lives[0].session.Character.MP != 100 || sinks[1].count(0x136) != 0 {
				t.Fatal("rejected magic applied partial damage/cost/foreign frame")
			}
			if kind == "rollback" && calls != 1 || kind != "rollback" && calls != 0 {
				t.Fatal("magic validation happened after mutation")
			}
			corrected := false
			for _, p := range buffPackets(sinks[0]) {
				corrected = corrected || bytes.Equal(p, protocol.UpdateHPMP(50, 100))
			}
			if !corrected {
				t.Fatal("known rejection did not restore native optimistic MP")
			}
		})
	}
}

func TestNativeSnailAdmissionUsesBodySweepAndRejectsUnknownGeometry(t *testing.T) {
	for _, q := range []struct {
		name     string
		x        int
		left     bool
		unknown  bool
		wantCall bool
	}{
		{name: "right beyond old radius", x: 280, wantCall: true},
		{name: "right last overlapping pixel", x: 310, wantCall: true},
		{name: "right edge only touches", x: 311},
		{name: "left last overlapping pixel", x: -310, left: true, wantCall: true},
		{name: "left edge only touches", x: -311, left: true},
		{name: "behind caster", x: -80},
		{name: "native muzzle gap", x: 0},
		{name: "unknown never falls back to point distance", x: 80, unknown: true},
	} {
		t.Run(q.name, func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			lives[0].session.Character.Skills = []domain.SkillState{{ID: 1000, Level: 1}}
			mob.life.X = q.x
			if q.unknown {
				mob.stats.ID = 7654321
			}
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(request domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return domain.CombatAttackResult{}, domain.ErrInsufficient
			}}
			a := worldMagic(t, mob.objectID, 10, 0, 0)
			if q.left {
				a.Display |= 0x8000
			}
			before := mob.hp
			if err := s.magicGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
				t.Fatal(err)
			}
			if (calls == 1) != q.wantCall || calls > 1 || mob.hp != before || mob.claiming || lives[0].session.Character.MP != 100 || sinks[1].count(0x136) != 0 {
				t.Fatalf("body admission/rollback differed: calls=%d want=%v hp=%d", calls, q.wantCall, mob.hp)
			}
		})
	}
}
