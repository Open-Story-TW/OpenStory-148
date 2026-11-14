package persistence

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestMistEruptionPGNativeWireCostReplayAndZeroTargets(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	ctx, s, actors := settlementFixture(t)
	if root == "" {
		t.Fatal("original WZ required")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	a := actors[0]
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET job=212,level=120,mp=500,base_max_mp=500,max_mp=500 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,2121003,30,30)`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	var e error
	a.Character, e = s.GetCharacter(ctx, a.CharacterID)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := hex.DecodeString(nativeEruptionBody)
	q := eruptionCommandForWire(t, body)
	q.Session = a
	q.Members = []domain.CharacterSession{a}
	q.OperationID = "native-mist-eruption-pg-operation"
	cat, e := content.DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	q.Targets[0].MobID = 9300306
	mob, e := cat.Mob(q.Targets[0].MobID)
	if e != nil || mob.HP < 10339 {
		t.Fatalf("real mob %+v %v", mob, e)
	}
	q.Targets[0].BeforeHP = mob.HP
	q.Targets[0].Contributions = []domain.CombatContribution{{CharacterID: a.CharacterID, SessionID: a.SessionID, Generation: a.Generation, Damage: int64(q.Targets[0].Damage)}}
	r, e := s.ApplyCombatAttack(ctx, q)
	if e != nil || len(r.Characters) != 1 || r.Characters[0].MP != 400 || len(r.Characters[0].SkillCooldowns) != 1 {
		t.Fatalf("actual native settlement %+v %v", r, e)
	}
	if again, e := s.ApplyCombatAttack(ctx, q); e != nil || !again.Replayed || again.Characters[0].MP != 400 {
		t.Fatalf("replay %+v %v", again, e)
	}
	hash, e := domain.CombatManifestHash(q)
	if e != nil {
		t.Fatal(e)
	}
	resolved, e := s.CombatAttackOutcome(ctx, a.Lease, q.OperationID, hash)
	if e != nil || resolved == nil || resolved.Characters[0].MP != 400 {
		t.Fatalf("same-op resolver %+v %v", resolved, e)
	}
	var raw []byte
	if e = s.db.QueryRowContext(ctx, `SELECT manifest FROM asset_combat_attacks WHERE operation_id=$1`, q.OperationID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var audit struct {
		Wire    []byte
		Targets []domain.CombatTarget
		MPCost  int
	}
	if e = json.Unmarshal(raw, &audit); e != nil || hex.EncodeToString(audit.Wire) != nativeEruptionBody || len(audit.Targets) != 1 || audit.Targets[0].Damage != 10339 || audit.MPCost != 100 {
		t.Fatalf("immutable audit %+v %v", audit, e)
	}
	// Same operation with a different selected mist is a different manifest.
	changed := q
	changed.Wire = append([]byte(nil), q.Wire...)
	changed.Wire[len(changed.Wire)-4]++
	if _, e = s.ApplyCombatAttack(ctx, changed); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("same-op changed area: %v", e)
	}
	a.Character = r.Characters[0]
	if _, e = s.db.ExecContext(ctx, `DELETE FROM game_skill_cooldowns WHERE character_id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	// A real zero-target shape still carries its selected mist and the five-
	// line attack type. It consumes one cast, without a damage or loot claim.
	zero := append(append([]byte(nil), body[:28]...), body[74:]...)
	zero[1] = 5
	z := eruptionCommandForWire(t, zero)
	z.Session = a
	z.Members = []domain.CharacterSession{a}
	z.OperationID = "native-mist-eruption-zero-operation"
	z.ClientTick++
	bad := z
	bad.OperationID += "-bad"
	copyCost := *z.Cost
	bad.Cost = &copyCost
	bad.Cost.MP = 99
	if _, e = s.ApplyCombatAttack(ctx, bad); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("wrong cost %v", e)
	}
	r, e = s.ApplyCombatAttack(ctx, z)
	if e != nil || len(r.Spawns) != 0 || r.Characters[0].MP != 300 {
		t.Fatalf("zero-target %+v %v", r, e)
	}
	if _, e = s.ApplyCombatAttack(ctx, z); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_attacks WHERE actor_id=$1`, a.CharacterID).Scan(&count); e != nil || count != 2 {
		t.Fatalf("receipt count %d %v", count, e)
	}
	loaded, e := s.GetCharacter(ctx, a.CharacterID)
	if e != nil || loaded.MP != 300 {
		t.Fatalf("durable MP %+v %v", loaded, e)
	}
}
