package persistence

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const nativeEruptionBody = "02152b5d20000000bd8a67002601f00d79a906069b08921e000000007d420f00078003017fffd70082ffd70038045d0a00006b080000a40300009a0700005d0a0000db323b5856b5031115ffd70000017e420f00"

func eruptionCommandForWire(t *testing.T, body []byte) domain.CombatAttackRequest {
	t.Helper()
	a, e := protocol.ReadAttack(protocol.RecvMagicAttack, protocol.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	q := domain.CombatAttackRequest{OperationID: "mist-eruption-wire-fixture", Family: domain.CombatSkill, SkillID: 2121003, ClientTick: a.Tick + 100, Members: []domain.CharacterSession{{CharacterID: 1}}, LinesPerTarget: a.LineCount(), Wire: append([]byte(nil), body...), Cost: &domain.CombatCost{Opcode: protocol.RecvMagicAttack, Level: 30, MP: 100, CooldownMS: 10000}}
	for _, target := range a.Targets {
		v := domain.CombatTarget{ObjectID: target.ObjectID, MobID: 9300018, SpawnOperationID: "mist-eruption-spawn-fixture", BeforeHP: 550000000}
		for _, line := range target.Lines {
			v.Lines = append(v.Lines, domain.CombatDamageLine{Raw: line.Raw, Applied: line.Damage})
			v.Damage += line.Damage
		}
		q.Targets = append(q.Targets, v)
	}
	return q
}

func TestMistEruptionCooldownProjectionIsDetachedAndKeepsOtherPayloadCompact(t *testing.T) {
	until := time.Now().Add(10 * time.Second)
	c := domain.Character{SkillCooldowns: []domain.SkillCooldown{{ID: 2121003, ExpiresAt: until}}, Items: []domain.InventoryItem{{ID: 1}}, SkillBuffs: []domain.SkillBuff{{ID: 2121004}}, Skills: []domain.SkillState{{ID: 2121003, Level: 30}}}
	p := combatProgressProjection(c)
	if len(p.SkillCooldowns) != 1 || p.SkillCooldowns[0].ExpiresAt != until || len(p.Items) != 0 || len(p.SkillBuffs) != 0 || len(p.Skills) != 0 {
		t.Fatalf("projection %+v", p)
	}
	p.SkillCooldowns[0].ID = 1
	if c.SkillCooldowns[0].ID != 2121003 {
		t.Fatal("receipt projection aliases source cooldown")
	}
}

func TestMistEruptionStoreWireBindsRawLinesAndOnlyReviewedFamily(t *testing.T) {
	body, _ := hex.DecodeString(nativeEruptionBody)
	body = protocol.UpgradeV145AttackBody(protocol.RecvMagicAttack, body) // archived v145 frame
	q := eruptionCommandForWire(t, body)
	if e := validateCombatCommand(q); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"skill", "opcode", "summon", "ongoing", "line_count", "raw", "order", "oid", "missing_target", "tail", "truncation"} {
		t.Run(kind, func(t *testing.T) {
			q := eruptionCommandForWire(t, body)
			switch kind {
			case "skill":
				q.SkillID = 2121007
			case "opcode":
				q.Cost.Opcode = protocol.RecvMeleeAttack
			case "summon":
				q.Cost.Summon = 1
			case "ongoing":
				q.Cost.Ongoing = true
			case "line_count":
				q.LinesPerTarget = 4
			case "raw":
				q.Targets[0].Lines[0].Raw++
			case "order":
				q.Targets[0].Lines[0], q.Targets[0].Lines[1] = q.Targets[0].Lines[1], q.Targets[0].Lines[0]
			case "oid":
				q.Targets[0].ObjectID++
			case "missing_target":
				q.Targets = nil
			case "tail":
				q.Wire[len(q.Wire)-5] = 0
			case "truncation":
				q.Wire = q.Wire[:len(q.Wire)-1]
			}
			if e := validateCombatCommand(q); !errors.Is(e, domain.ErrInvalidInput) {
				t.Fatalf("accepted %s: %v", kind, e)
			}
		})
	}
	// Two targets are encoded high-OID first. Store sorts by OID before
	// validation, while within-target line order is never changed.
	second := append([]byte(nil), body[28:78]...) // v148 target: 4+18+5*4+8
	binary.LittleEndian.PutUint32(second, uint32(q.Targets[0].ObjectID-1))
	two := append(append(append([]byte(nil), body[:78]...), second...), body[78:]...)
	two[1] = 0x25
	q = eruptionCommandForWire(t, two)
	q.Targets[0], q.Targets[1] = q.Targets[1], q.Targets[0]
	q.Targets[0].SpawnOperationID += "-second"
	if e := validateCombatCommand(q); e != nil {
		t.Fatalf("sorted target binding: %v", e)
	}
	// Historical generic commands without raw attribution remain readable.
	q.Wire = nil
	if e := validateCombatCommand(q); e != nil {
		t.Fatalf("legacy omitted body: %v", e)
	}
}
