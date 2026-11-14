package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func captureNativeDamageEvidence(t *testing.T, action func()) map[string]any {
	t.Helper()
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "1")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	action()
	var found []map[string]any
	for _, line := range bytes.Split(logs.Bytes(), []byte{'\n'}) {
		var row map[string]any
		if json.Unmarshal(line, &row) == nil && row["msg"] == "combat_damage_committed" {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("wanted one actual damage evidence row, got%d: %s", len(found), logs.Bytes())
	}
	return found[0]
}

func assertNativeDamageWire(t *testing.T, row map[string]any, body []byte, tick uint32) {
	t.Helper()
	hash := sha256.Sum256(body)
	if row["wire_body_sha256"] != hex.EncodeToString(hash[:]) || row["client_tick"] != float64(tick) || row["server_generated"] != false {
		t.Fatalf("native body attribution changed: %+v", row)
	}
	if row["proof"] != "actual_server_hp_mutation" {
		t.Fatal("wire hash alone was mistaken for applied damage")
	}
}

func evidenceMeleeBody(skill int, tick uint32, objectID int32, claimed int32) []byte {
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
	w.U32(tick)
	w.U32(0)
	w.U32(0) // v148 melee i32 before the targets
	w.I32(objectID)
	w.Zero(18) // v148 target metadata
	w.I32(claimed)
	w.Zero(8)
	w.I16(0)
	w.I16(0)
	return w.Packet()
}

func TestDamageEvidenceLegacyNativeBodyHashAndTick(t *testing.T) {
	for _, skill := range []int{0, 1001004, 1001005} {
		for _, claimed := range []int32{3, 100000} {
			t.Run(fmt.Sprintf("%d/%d", skill, claimed), func(t *testing.T) {
				s, lives, sinks, mob := sharedWorldFixture(t)
				mob.hp = 1000
				lives[0].session.Character.Skills = []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}
				s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
				const tick = 0x11223344
				body := evidenceMeleeBody(skill, tick, mob.objectID, claimed)
				original := append([]byte(nil), body...)
				a, err := protocol.ReadMeleeAttack(protocol.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body[0] ^= 0xff
				family := domain.CombatNormal
				if skill != 0 {
					family = domain.CombatPhysicalSkill
				}
				row := captureNativeDamageEvidence(t, func() {
					if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], a, family); err != nil {
						t.Fatal(err)
					}
				})
				assertNativeDamageWire(t, row, original, tick)
				removed := 1000 - mob.hp
				if removed <= 0 || claimed == 3 && removed != 3 || claimed == 100000 && removed >= int64(claimed) {
					t.Fatal("original damage/capping behavior changed")
				}
				assertLegacyEvidenceLines(t, row, claimed, int(removed))
			})
		}
	}
}

func assertLegacyEvidenceLines(t *testing.T, row map[string]any, claimed int32, applied int) {
	t.Helper()
	targets, ok := row["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("missing target evidence: %+v", row)
	}
	target := targets[0].(map[string]any)
	raw, rawOK := target["claimed_lines_raw"].([]any)
	bounded, boundedOK := target["bounded_lines"].([]any)
	if !rawOK || !boundedOK || len(raw) != 1 || len(bounded) != 1 || raw[0] != float64(claimed) || bounded[0] != float64(applied) || target["hp_removed"] != float64(applied) {
		t.Fatalf("raw line was lost/replaced by its capped value: %+v", target)
	}
}

func TestLegacyLineEvidenceUnknownReceiptRetainsSameManifest(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = []domain.SkillState{{ID: 1001004, Level: 1}}
	body := evidenceMeleeBody(1001004, 54321, mob.objectID, 100000)
	a, err := protocol.ReadMeleeAttack(protocol.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var frozen domain.CombatAttackRequest
	var stored domain.CombatAttackResult
	ready, applies := false, 0
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		applies++
		frozen, stored = q, fakeSharedResult(q)
		return domain.CombatAttackResult{}, errors.New("commit reply lost")
	}, outcome: func(op, hash string) (*domain.CombatAttackResult, error) {
		actual, e := domain.CombatManifestHash(frozen)
		if e != nil || op != frozen.OperationID || hash != stored.ManifestHash || hash != actual {
			t.Fatal("same-op resolution changed the frozen line manifest")
		}
		if !ready {
			return nil, errors.New("lookup unavailable")
		}
		return &stored, nil
	}}
	s.repo = repo
	before := mob.hp
	if err = s.physicalSkillGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if applies != 1 || len(frozen.Targets) != 1 || len(frozen.Targets[0].Lines) != 1 || frozen.Targets[0].Lines[0].Raw != 100000 || frozen.Targets[0].Lines[0].Applied != int(before) {
		t.Fatalf("not the original/capped frozen line: %+v", frozen.Targets)
	}
	ready = true
	row := captureNativeDamageEvidence(t, func() { s.resolveCombatSettlements(context.Background(), repo) })
	assertNativeDamageWire(t, row, body, 54321)
	assertLegacyEvidenceLines(t, row, 100000, int(before))
	if applies != 1 || mob.hp != 0 {
		t.Fatal("receipt resolution re-executed the attack or changed its HP cap")
	}
}

func TestDamageEvidenceSummonNativeBodyHashAndTick(t *testing.T) {
	s, lives, sinks, mob, summonID := summonFixture(t)
	mob.hp = 1000
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	const tick = 0x55667788
	w := protocol.NewRawWriter()
	w.I32(summonID)
	w.U32(tick)
	w.U8(3)
	w.U8(1)
	w.I16(0)
	w.I16(0)
	w.Zero(8)
	w.I32(mob.objectID)
	w.Zero(4 + 18)    // v148 mob template ID, then the metadata
	w.U32(0x80000007) // Preserve critical bit while applying only seven damage.
	w.U32(0xdeadbeef)
	body := w.Packet()
	original := append([]byte(nil), body...)
	in, err := protocol.ReadSummonAttack(protocol.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	body[0] ^= 0xff
	row := captureNativeDamageEvidence(t, func() {
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
	})
	assertNativeDamageWire(t, row, original, tick)
	target := row["targets"].([]any)[0].(map[string]any)
	if target["claimed_lines_raw"].([]any)[0] != float64(0x80000007) || target["bounded_lines"].([]any)[0] != float64(7) {
		t.Fatal("summon critical raw line was lost or counted as numeric damage")
	}
	if mob.hp != 993 {
		t.Fatal("summon wire hash was logged without its actual HP write")
	}
}

func TestDamageEvidenceServerGeneratedHitHasNoNativeBodyAttribution(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	mob.hp = 1000
	s.repo = &sharedCombatRepo{}
	row := captureNativeDamageEvidence(t, func() {
		if err := s.DamageMobs(context.Background(), 1, lives[0], sinks[0], 1001005, []combat.ServerHit{{ObjectID: mob.objectID, Damage: 7}}); err != nil {
			t.Fatal(err)
		}
	})
	if row["server_generated"] != true || row["wire_body_sha256"] != nil || mob.hp != 993 {
		t.Fatalf("server-generated damage acquired false native proof: %+v hp%d", row, mob.hp)
	}
}
