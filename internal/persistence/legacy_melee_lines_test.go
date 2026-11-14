package persistence

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Original native blue-snail-03 frame: skill0, one target, raw165. The
// monster has20HP; preserving Raw165 must not make its bounded20 invalid.
const legacySnailKillWire = "4b00011100000000000000000000070051955cc9010459ec5c1e0000000043420f00070100011e00d7001e00d7008901a5000000458835b42667347ef1ffd700"

func TestLegacyMeleeLineAuditRetainsBoundedKillAndRejectsForgery(t *testing.T) {
	for _, family := range []domain.CombatFamily{domain.CombatNormal, domain.CombatPhysicalSkill} {
		q := domain.CombatAttackRequest{Family: family, OperationID: "legacy-line-shape-test", Members: []domain.CharacterSession{{CharacterID: 1}}, Targets: []domain.CombatTarget{{ObjectID: 1000003, MobID: 100101, SpawnOperationID: "native-blue-snail-spawn", BeforeHP: 20, Damage: 20, Defeated: true, Lines: []domain.CombatDamageLine{{Raw: 165, Applied: 20}}}}}
		if family == domain.CombatPhysicalSkill {
			q.SkillID = 1001004
		}
		if err := validateCombatCommand(q); err != nil {
			t.Fatal("valid legacy raw/bounded audit rejected", family, err)
		}
		for _, line := range []domain.CombatDamageLine{
			{Raw: 165, Applied: 19}, {Raw: 19, Applied: 20}, {Raw: 0x800000a5, Applied: 20},
			{Raw: 165, Applied: -1}, {Raw: 165, Applied: 20, Criticality: domain.CombatCriticalUnknown},
			{Raw: 165, Applied: 20, Criticality: domain.CombatCriticalExplicit},
		} {
			q.Targets[0].Lines = []domain.CombatDamageLine{line}
			if err := validateCombatCommand(q); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatal("forged legacy line accepted", family, line, err)
			}
		}
		q.Targets[0].Lines = []domain.CombatDamageLine{{Raw: 165, Applied: 20}, {Raw: 0, Applied: 0}}
		if err := validateCombatCommand(q); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("multiline legacy accepted", err)
		}
		q.Targets[0].Lines = nil
		if err := validateCombatCommand(q); err != nil {
			t.Fatal("old zero-line compatibility rejected", err)
		}
	}
}

func TestLegacyMeleeNativeSnailKillPersistsRawAndLootOnce(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	ctx, store, actors := settlementFixture(t)
	if root == "" {
		t.Fatal("original WZ root required for native blue-snail regression")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	a := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET job=0,str=300,dex=6,int_stat=4,luk=4,exp=0 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE asset_equipment SET item_id=1302000,watk=17 WHERE character_id=$1 AND slot=-11`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mob, err := catalog.Mob(100101)
	if err != nil || mob.HP != 20 {
		t.Fatal("original blue-snail HP", mob, err)
	}
	wire, _ := hex.DecodeString(legacySnailKillWire)
	attack, err := protocol.ReadMeleeAttack(protocol.NewReader(wire[2:]))
	if err != nil || len(attack.Targets) != 1 || attack.Targets[0].Damage != 165 {
		t.Fatal("actual native frame", attack, err)
	}
	q := domain.CombatAttackRequest{Session: a, Members: []domain.CharacterSession{a}, OperationID: "native-snail-legacy-kill", ClientTick: attack.Tick, Family: domain.CombatNormal, Targets: []domain.CombatTarget{{ObjectID: attack.Targets[0].ObjectID, MobID: mob.ID, SpawnOperationID: "native-snail-legacy-spawn", X: 30, Y: 215, BeforeHP: 20, Damage: 20, Defeated: true, Contributions: []domain.CombatContribution{{CharacterID: a.CharacterID, SessionID: a.SessionID, Generation: a.Generation, Damage: 20}}, Lines: []domain.CombatDamageLine{{Raw: 165, Applied: 20}}}}}
	q.Parties, err = store.CombatParties(ctx, a.Lease, []int64{a.CharacterID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyCombatAttack(ctx, q)
	if err != nil || len(result.Spawns) != 1 || len(result.Characters) != 1 || result.Characters[0].EXP != mob.EXP {
		t.Fatal("native kill rejected/reward absent", result, err)
	}
	var raw []byte
	if err := store.db.QueryRowContext(ctx, `SELECT manifest FROM asset_combat_attacks WHERE operation_id=$1`, q.OperationID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Targets []domain.CombatTarget }
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Targets) != 1 || manifest.Targets[0].Lines[0].Raw != 165 || manifest.Targets[0].Lines[0].Applied != 20 {
		t.Fatal("raw/bounded evidence lost", string(raw))
	}
	if again, err := store.ApplyCombatAttack(ctx, q); err != nil || !again.Replayed {
		t.Fatal("same attack replay", again, err)
	}
	hash, _ := domain.CombatManifestHash(q)
	if outcome, err := store.CombatAttackOutcome(ctx, a.Lease, q.OperationID, hash); err != nil || outcome == nil || !outcome.Replayed {
		t.Fatal("same-op resolution", outcome, err)
	}
	var rewards, loot int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_rewards WHERE operation_id=$1`, q.Targets[0].SpawnOperationID).Scan(&rewards); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs WHERE operation_id=$1`, q.Targets[0].SpawnOperationID).Scan(&loot); err != nil || rewards != 1 || loot != 1 {
		t.Fatal("duplicate/missing reward or loot", rewards, loot, err)
	}
	q.Targets[0].Lines[0].Raw++
	if _, err := store.ApplyCombatAttack(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("same key changed raw", err)
	}
}
