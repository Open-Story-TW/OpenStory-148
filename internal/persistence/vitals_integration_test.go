package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestBaseVitalsEquipmentRoundTripPacketAndCheckpoint(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_equipment SET hp=20,mp=7 WHERE character_id=$1 AND slot=-11`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || a.Character.BaseMaxHP != 50 || a.Character.MaxHP != 70 || a.Character.BaseMaxMP != 5 || a.Character.MaxMP != 12 {
		t.Fatalf("SQL derived load %+v %v", a.Character, err)
	}
	entValue, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || entValue.MaxHP != 70 || entValue.MaxMP != 12 || entValue.BaseMaxHP != 50 {
		t.Fatalf("Ent derived differs %+v %v", entValue, err)
	}
	value := a.Character
	value.HP = 65
	value.MP = 10
	if err = s.SaveCharacter(ctx, a, value); err != nil {
		t.Fatal(err)
	}
	value.Revision++
	a.Character = value
	forged := value
	forged.HP = 71
	forged.MaxHP = 99999
	if err = s.SaveCharacter(ctx, a, forged); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("checkpoint forged effective cap accepted %v", err)
	}
	forged = value
	forged.Equipment = append([]domain.Equipment(nil), value.Equipment...)
	forged.Equipment[0].HP += 1000
	forged.Job = 100
	forged.Skills = []domain.SkillState{{ID: 1000006, Level: 10}}
	forged.HP = 71
	if err = s.SaveCharacter(ctx, a, forged); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("checkpoint accepted cap from unpersisted equipment or skill: %v", err)
	}
	forged = value
	forged.BaseMaxHP++
	if err = s.SaveCharacter(ctx, a, forged); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("checkpoint rewrote intrinsicHP %v", err)
	}
	r, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 100, InvType: 1, Source: -11, Destination: 1, Quantity: 1})
	if err != nil || r.Character.BaseMaxHP != 50 || r.Character.MaxHP != 50 || r.Character.HP != 50 || r.Character.BaseMaxMP != 5 || r.Character.MaxMP != 5 || r.Character.MP != 5 {
		t.Fatalf("unequip base/cap/clamp %+v %v", r.Character, err)
	}
	a.Character = r.Character
	r, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 101, InvType: 1, Source: 1, Destination: -11, Quantity: 1})
	if err != nil || r.Character.BaseMaxHP != 50 || r.Character.MaxHP != 70 || r.Character.HP != 50 || r.Character.MaxMP != 12 || r.Character.MP != 5 {
		t.Fatalf("equip compounded or healed %+v %v", r.Character, err)
	}
	p := protocol.NewReader(protocol.InventoryVitals(r.Character)[11:])
	if p.I32() != 50 || p.I32() != 50 || p.I32() != 5 || p.I32() != 5 {
		t.Fatal("native stat update sent effective maxima in intrinsic fields")
	}
	var raw []byte
	if err = s.db.QueryRowContext(ctx, `SELECT snapshot FROM game_character_roster WHERE character_id=$1`, a.CharacterID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var roster domain.Character
	if err = json.Unmarshal(raw, &roster); err != nil || roster.BaseMaxHP != 50 || roster.MaxHP != 70 || roster.VitalsModel != 1 || len(roster.Skills) != 0 || len(roster.Items) != 0 {
		t.Fatalf("lean roster lost model fields %+v %v", roster, err)
	}
}

func TestHPIncreaseUsesDerivedMaxWithoutChangingBaseOrHealing(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=10,sp=2,base_max_hp=100,max_hp=100,hp=80 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{102, 104} {
		r, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1000006, OperationID: fmt.Sprintf("hp-increase-%d-%d", a.CharacterID, i)})
		if e != nil || r.Character.BaseMaxHP != 100 || r.Character.MaxHP != want || r.Character.HP != 80 || r.Character.SP != 1-i {
			t.Fatalf("passive source/growth %+v %v", r, e)
		}
		a.Character = r.Character
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.BaseMaxHP != 100 || loaded.MaxHP != 104 || loaded.HP != 80 {
		t.Fatalf("passive reload %+v %v", loaded, err)
	}
}

func TestBaseVitalsMigrationAuditsCompatibilityAndPreservesAmbiguity(t *testing.T) {
	ctx, s, _ := inventoryFixture(t)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	schema := "vitals_audit_" + suffix
	if _, err = tx.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	setup := `CREATE TABLE game_characters(id bigint PRIMARY KEY,revision bigint,hp integer,mp integer,max_hp integer,max_mp integer);
CREATE TABLE asset_equipment(character_id bigint,slot integer,hp integer,mp integer);
CREATE TABLE game_character_roster(character_id bigint,snapshot jsonb);
INSERT INTO game_characters VALUES(1,7,70,10,100,20),(2,8,100,0,99999,0),(3,9,99999,5,100020,100),(4,10,5,3,10,5);
INSERT INTO asset_equipment VALUES(1,-11,20,5),(1,1,9999,9999),(2,-111,20,0),(3,-11,20,0),(4,-11,20,0);
INSERT INTO game_character_roster SELECT id,'{}'::jsonb FROM game_characters;`
	if _, err = tx.ExecContext(ctx, setup); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "ent", "migrate", "migrations", "0033_base_vitals.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(source)); err != nil {
		t.Fatal(err)
	}
	var baseHP, baseMP, oldHP, oldMP, model int
	if err = tx.QueryRowContext(ctx, `SELECT base_max_hp,base_max_mp,hp,mp,vitals_model FROM game_characters WHERE id=1`).Scan(&baseHP, &baseMP, &oldHP, &oldMP, &model); err != nil || baseHP != 80 || baseMP != 15 || oldHP != 70 || oldMP != 10 || model != 1 {
		t.Fatalf("compatibility inputs changed %d/%d %d/%d model%d %v", baseHP, baseMP, oldHP, oldMP, model, err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT base_max_hp,base_max_mp FROM game_characters WHERE id=2`).Scan(&baseHP, &baseMP); err != nil || baseHP != 99979 || baseMP != 0 {
		t.Fatalf("cash wear/cap inference wrong %d/%d %v", baseHP, baseMP, err)
	}
	var preserved, rows int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM game_characters WHERE id IN(3,4) AND vitals_model=0 AND base_max_hp IS NULL AND base_max_mp IS NULL AND max_hp IN(100020,10)`).Scan(&preserved); err != nil || preserved != 2 {
		t.Fatalf("ambiguous history was rewritten %d %v", preserved, err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM ops_vitals_backfill WHERE old_hp IN(70,100,99999,5) AND policy='legacy_effective_minus_worn_slots_-199_to_-1_v1'`).Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("migration inputs not audited %d %v", rows, err)
	}
	if _, err = tx.ExecContext(ctx, `SAVEPOINT invalid_model`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET vitals_model=1,base_max_hp=NULL,base_max_mp=NULL WHERE id=4`); err == nil {
		t.Fatal("CHECK accepted known model with NULL bases")
	}
	if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT invalid_model`); err != nil {
		t.Fatal(err)
	}
}

func TestBaseVitalsOfflineTimedPassiveClampAndAmbiguousPreservation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=10,base_max_hp=100,max_hp=120,hp=120 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level,expires_at)VALUES($1,1000006,10,0,$2)`, a.CharacterID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, entRead := range []bool{false, true} {
		var c domain.Character
		var err error
		if entRead {
			c, err = s.GetCharacter(ctx, a.CharacterID)
		} else {
			c, err = readCharacterSQL(ctx, s.db, a.CharacterID)
		}
		if err != nil || c.BaseMaxHP != 100 || c.MaxHP != 100 || c.HP != 100 {
			t.Fatalf("expired passive initialread ent=%v %+v %v", entRead, c, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET hp=0 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	dead, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || dead.HP != 0 {
		t.Fatalf("offline normalization revived dead character %+v %v", dead, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET vitals_model=0,base_max_hp=NULL,base_max_mp=NULL,vitals_origin='test_ambiguous_preserved',hp=120,max_hp=130 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || ambiguous.MaxHP != 130 || ambiguous.HP != 120 || ambiguous.VitalsModel != 0 {
		t.Fatalf("unknown base was silently normalized %+v %v", ambiguous, err)
	}
}
