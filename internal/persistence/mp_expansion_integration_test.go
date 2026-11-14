package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
	"time"
)

func TestMPExpansionAtomicAllocationReplayRollbackAndEquipment(t *testing.T) {
	useCombatSchema(t)
	ctx, store, actors := inventoryFixture(t)
	a := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET job=200,level=10,sp=11,base_max_mp=100,max_mp=120,mp=40 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE asset_equipment SET mp=20 WHERE character_id=$1 AND slot=-11`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	beforeBase, beforeMP := a.Character.BaseMaxMP, a.Character.MP
	request := domain.SkillAllocateRequest{Session: a, SkillID: 2000006, OperationID: "mp-expansion-first-allocation"}
	result, err := store.AllocateSkill(ctx, request)
	if err != nil || !result.Changed || result.Character.MaxMP != 122 || result.Character.BaseMaxMP != beforeBase || result.Character.MP != beforeMP || result.Character.SP != 10 {
		t.Fatalf("atomic allocate %+v %v", result, err)
	}
	replay, err := store.AllocateSkill(ctx, request)
	if err != nil || !replay.Replayed || replay.Changed || replay.Character.Revision != result.Character.Revision || replay.Character.MaxMP != 122 || replay.Character.SP != 10 {
		t.Fatalf("replay %+v %v", replay, err)
	}
	a.Character = result.Character
	stale := request
	stale.Session = a
	stale.Session.Generation++
	stale.OperationID = "mp-expansion-stale-owner"
	if _, err = store.AllocateSkill(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale allocation", err)
	}
	// Receipt insertion occurs after SP, learned state, aggregate and roster writes.
	// Inject an error there to prove the whole command rolls back in PostgreSQL.
	if _, err = store.db.ExecContext(ctx, `CREATE FUNCTION mp_expansion_fail_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='mp-expansion-forced-rollback' THEN RAISE EXCEPTION 'intentional MP allocation rollback'; END IF; RETURN NEW; END $$;CREATE TRIGGER mp_expansion_fail_receipt BEFORE INSERT ON asset_skill_commands FOR EACH ROW EXECUTE FUNCTION mp_expansion_fail_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 2000006, OperationID: "mp-expansion-forced-rollback"}); err == nil {
		t.Fatal("forced insert failure committed")
	}
	loaded, err := store.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.SP != 10 || loaded.Revision != a.Character.Revision || loaded.MaxMP != 122 || learned(loaded, 2000006).Level != 1 {
		t.Fatalf("rollback lost atomicity %+v %v", loaded, err)
	}
	for level := 2; level <= 10; level++ {
		result, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 2000006, OperationID: fmt.Sprintf("mp-expansion-level-%02d", level)})
		if err != nil || result.Character.MP != beforeMP || result.Character.BaseMaxMP != beforeBase || result.Character.MaxMP != 120+120*(2*level)/100 {
			t.Fatalf("level%d %+v %v", level, result, err)
		}
		a.Character = result.Character
	}
	if _, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 2000006, OperationID: "mp-expansion-above-max"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("max level accepted", err)
	}
	if _, err = store.CastSkill(ctx, domain.SkillCastRequest{Session: a, SkillID: 2000006, ClientLevel: 10, OperationID: "mp-expansion-passive-cast"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("passive cast allowed", err)
	}
	move, err := store.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 2100, InvType: 1, Source: -11, Destination: 1, Quantity: 1})
	if err != nil || move.Character.MaxMP != 120 || move.Character.BaseMaxMP != 100 || move.Character.MP != 40 {
		t.Fatalf("unequip compounded/healed %+v %v", move, err)
	}
	a.Character = move.Character
	move, err = store.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 2101, InvType: 1, Source: 1, Destination: -11, Quantity: 1})
	if err != nil || move.Character.MaxMP != 144 || move.Character.BaseMaxMP != 100 || move.Character.MP != 40 {
		t.Fatalf("equip compounded/healed %+v %v", move, err)
	}
	a.Character = move.Character
	packet := protocol.NewReader(protocol.InventoryVitals(a.Character)[11:])
	_ = packet.I32()
	_ = packet.I32()
	if packet.I32() != 40 || packet.I32() != 100 {
		t.Fatal("effective maximum leaked into native intrinsic MP field")
	}
	for _, ent := range []bool{false, true} {
		var c domain.Character
		if ent {
			c, err = store.GetCharacter(ctx, a.CharacterID)
		} else {
			c, err = readCharacterSQL(ctx, store.db, a.CharacterID)
		}
		if err != nil || c.MaxMP != 144 || c.BaseMaxMP != 100 || c.MP != 40 || learned(c, 2000006).Level != 10 {
			t.Fatalf("relogin ent%v %+v %v", ent, c, err)
		}
	}
	// An offline expiring passive must lower maximum and clamp existing MP on
	// both initial SQL and Ent snapshots, without changing intrinsic MP.
	if _, err = store.db.ExecContext(ctx, `UPDATE game_skills SET level=1,expires_at=$2 WHERE character_id=$1 AND skill_id=2000006`, a.CharacterID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET mp=144 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, ent := range []bool{false, true} {
		var c domain.Character
		if ent {
			c, err = store.GetCharacter(ctx, a.CharacterID)
		} else {
			c, err = readCharacterSQL(ctx, store.db, a.CharacterID)
		}
		if err != nil || c.MaxMP != 120 || c.BaseMaxMP != 100 || c.MP != 120 {
			t.Fatalf("expiry ent%v %+v %v", ent, c, err)
		}
		a.Character = c
	}
	if _, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 2000006, OperationID: "mp-expansion-expired-no-sp"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("expired passive consumed SP", err)
	}
	other := actors[1]
	if _, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: other, SkillID: 2000006, OperationID: "mp-expansion-wrong-job"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("beginner allocation", err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET job=200,level=10,sp=5,vitals_model=0,base_max_hp=NULL,base_max_mp=NULL WHERE id=$1`, other.CharacterID); err != nil {
		t.Fatal(err)
	}
	other.Character, err = store.GetCharacter(ctx, other.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: other, SkillID: 2000006, OperationID: "mp-expansion-ambiguous-base"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("ambiguous base allocation", err)
	}
}

func TestQAGMMPExpansionFreshGrantAtomicSetupAndNoPlayerGrant(t *testing.T) {
	ctx, store, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, store, actors[0])
	normal := actors[1]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET job=200,level=10,sp=3,base_max_mp=100,max_mp=100,mp=40 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req := domain.GMCommandRequest{Session: a, Name: "skill", Args: []int64{2000006, 10}, OperationID: "qa-mp-expansion-reviewed-setup"}
	result, err := store.ExecuteGMCommand(ctx, req)
	if err != nil || !result.Changed || result.SkillID != 2000006 || result.Character.MaxMP != 120 || result.Character.BaseMaxMP != 100 || result.Character.MP != 40 || result.Character.SP != 3 {
		t.Fatalf("QA MP %+v %v", result, err)
	}
	replay, err := store.ExecuteGMCommand(ctx, req)
	if err != nil || !replay.Replayed || replay.Changed || replay.Character.Revision != result.Character.Revision {
		t.Fatalf("QA replay %+v %v", replay, err)
	}
	req.Session = normal
	req.OperationID = "qa-mp-expansion-normal-refused"
	if _, err = store.ExecuteGMCommand(ctx, req); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("normal account gained QA passive", err)
	}
}
