package world

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func nativePotentialMeleeBody(skill int, oid int32, raw uint32) []byte {
	w := protocol.NewRawWriter()
	w.U8(0)
	w.U8(0x11)
	w.I32(int32(skill))
	w.Zero(2)
	w.U16(0)
	w.U8(0)
	w.U8(1)
	w.U16(35)
	w.Zero(2)
	w.Zero(3)
	w.U8(6)
	w.U32(99)
	w.U32(0)
	w.U32(0) // v148 melee i32 before the targets
	w.I32(oid)
	w.Zero(18) // v148 target metadata
	w.U32(raw)
	w.Zero(8)
	w.I16(0)
	w.I16(0)
	return w.Packet()
}

func TestReviewedLegacyCriticalNativeFramesUseGenericSettlementAndForeignRaw(t *testing.T) {
	for _, skill := range []int{0, 1001004, 1001005} {
		t.Run(fmt.Sprint(skill), func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			combat.SetStatsProvider(skills.Stats)
			t.Cleanup(func() { combat.SetStatsProvider(nil) })
			c := &lives[0].session.Character
			c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}
			c.SkillBuffs = []domain.SkillBuff{{ID: 3121002, Stat: "SHARP_EYES", Value: 10<<8 | 20, ExpiresAt: time.Now().Add(time.Minute)}}
			// An existing Sharp Eyes source also takes this path, without any
			// potential metadata. The same raw critical flag is never sent to the
			// compatibility signed-I32 decoder.
			body := nativePotentialMeleeBody(skill, mob.objectID, 0x800003e8)
			if _, err := protocol.ReadMeleeAttack(protocol.NewReader(body)); err == nil {
				t.Fatal("fixture failed to exercise the old critical rejection")
			}
			parsed, err := protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(body))
			if err != nil || parsed.Targets[0].Lines[0].Raw != 0x800003e8 || !bytes.Equal(parsed.Wire, body) {
				t.Fatal("generic native shape mismatch", parsed, err)
			}
			calls := 0
			var request domain.CombatAttackRequest
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				request = q
				r := fakeSharedResult(q)
				for i := range r.Characters {
					if r.Characters[i].ID == q.Session.CharacterID {
						r.Characters[i].MP -= q.Cost.MP
					}
				}
				return r, nil
			}}
			handled, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, body)
			if err != nil || !handled || calls != 1 || request.Family != domain.CombatSkill || len(request.Targets) != 1 || request.Targets[0].Lines[0].Raw != 0x800003e8 || mob.hp != 0 {
				t.Fatalf("critical frame lost %v %v calls%d req%+v mobHP%d", handled, err, calls, request, mob.hp)
			}
			cost := 0
			// Original Skill.wz100: Power Strike3+2*u(x/5)=5,
			// Slash Blast6+2*d(x/5)=6 at level1.
			if skill == 1001004 {
				cost = 5
			} else if skill != 0 {
				cost = 6
			}
			if c.MP != 100-cost || request.Cost.MP != cost || c.EXP != 3 {
				t.Fatal("generic cost/reward mismatch", c.MP, c.EXP, request.Cost)
			}
			found := false
			for _, packet := range sinks[1].packets {
				r := protocol.NewReader(packet)
				if r.U16() != protocol.SendMeleeAttack {
					continue
				}
				r.I32()
				r.U8()
				r.U8()
				if level := r.U8(); level != 0 {
					r.I32()
				}
				r.U8()
				r.U16()
				r.U8()
				r.U8()
				r.I32()
				if r.I32() != mob.objectID {
					t.Fatal("wrong foreign target")
				}
				r.U8()
				line := r.U32()
				if r.Err() != nil || line&0x80000000 == 0 || int(line&0x7fffffff) != request.Targets[0].Lines[0].Applied {
					t.Fatal("foreign critical/applied value lost", line, request.Targets[0].Lines)
				}
				found = true
			}
			if !found {
				t.Fatal("peer received no foreign attack")
			}
		})
	}
}

func TestPotentialNoncriticalRoutesGenericWhileAbsentRetainsLegacy(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	combat.SetStatsProvider(skills.Stats)
	t.Cleanup(func() { combat.SetStatsProvider(nil) })
	body := nativePotentialMeleeBody(0, mob.objectID, 1000)
	job := lives[0].session.Character.Job
	lives[0].session.Character.Job = 0
	if handled, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, body); err != nil || handled {
		t.Fatal("ordinary no-potential route changed", handled, err)
	}
	lives[0].session.Character.Job = job
	root := t.TempDir()
	for _, dir := range []string{"Item.wz", "Character.wz/Weapon"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Item.wz/ItemOption.img.xml"), []byte(`<imgdir name="ItemOption.img"><imgdir name="010001"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incSTR" value="10"/></imgdir></imgdir></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Character.wz/Weapon/01302000.img.xml"), []byte(`<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	lives[0].session.Character.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10001}}
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if q.Family != domain.CombatSkill || q.Cost.MP != 0 {
			t.Fatal("wrong active-potential route", q)
		}
		return fakeSharedResult(q), nil
	}}

	for _, state := range []uint8{0x10, 0x15, 0x1f} {
		lives[0].session.Character.Equipment[0].Potential.RawState = state
		handled, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, body)
		if err != nil || !handled || calls != 0 || mob.hp != 15 {
			t.Fatal("unknown state admitted/fell back to legacy", state, handled, err, calls, mob.hp)
		}
	}
	lives[0].session.Character.Equipment[0].Potential.RawState = 17
	if handled, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, body); err != nil || !handled || calls != 1 || mob.hp != 0 {
		t.Fatal("potential fallback remained signed legacy", handled, err, calls, mob.hp)
	}
}
