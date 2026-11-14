package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"reflect"
	"sync"
	"testing"
)

func TestSpearmanAtomicAdvancementReceiptReplayResolveAndReload(t *testing.T) {
	useCombatSchema(t)
	ctx, store, actors := inventoryFixture(t)
	a, b := actors[0], actors[1]
	for _, actor := range actors {
		if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=30,sp=5,map_id=103000003,base_max_hp=500,base_max_mp=100,max_hp=624,max_mp=107,hp=100,mp=20,str=90,dex=30,ap=12 WHERE id=$1`, actor.CharacterID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE asset_equipment SET hp=20,mp=7 WHERE character_id=$1 AND slot=-5`, actor.CharacterID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level)VALUES($1,1000006,10,0),($1,1001004,1,0),($1,1001005,1,0)`, actor.CharacterID); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	b.Character, err = store.GetCharacter(ctx, b.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	before := a.Character
	request := domain.JobAdvancementRequest{Session: a, OperationID: "spearman-atomic-source-operation", NPCID: 1052001, Script: "npc/1052001.lua", TargetJob: 130}
	result, err := store.AdvanceSecondJob(ctx, request)
	if err != nil || result.FromJob != 100 || result.ToJob != 130 || result.Character.Job != 130 || result.Character.SP != 6 || result.Character.BaseMaxHP < 800 || result.Character.BaseMaxHP > 850 || result.Character.MaxHP != (result.Character.BaseMaxHP+20)*120/100 || result.Character.BaseMaxMP != 100 || result.Character.MaxMP != 107 || result.Character.HP != result.Character.MaxHP || result.Character.MP != 107 || result.Character.STR != before.STR || result.Character.DEX != before.DEX || result.Character.AP != before.AP || !reflect.DeepEqual(result.Character.Skills, before.Skills) || !reflect.DeepEqual(result.Character.Equipment, before.Equipment) || len(result.Changes) != 0 || result.Character.InventorySlots != before.InventorySlots {
		t.Fatalf("advancement %+v %v", result, err)
	}
	replay, err := store.AdvanceSecondJob(ctx, request)
	if err != nil || !replay.Replayed || replay.Character.SP != 6 || replay.Character.Revision != result.Character.Revision {
		t.Fatalf("replay %+v %v", replay, err)
	}
	resolved, found, err := store.ResolveSecondJob(ctx, request)
	if err != nil || !found || resolved.Character.Job != 130 || resolved.Character.Revision != result.Character.Revision {
		t.Fatalf("same-op recovery %+v %v %v", resolved, found, err)
	}
	wrong := request
	wrong.TargetJob = 110
	if _, err = store.AdvanceSecondJob(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("arbitrary second job", err)
	}
	for _, ent := range []bool{false, true} {
		var c domain.Character
		if ent {
			c, err = store.GetCharacter(ctx, a.CharacterID)
		} else {
			c, err = readCharacterSQL(ctx, store.db, a.CharacterID)
		}
		if err != nil || c.Job != 130 || c.BaseMaxHP != result.Character.BaseMaxHP || c.MaxHP != result.Character.MaxHP || c.SP != 6 || len(protocol.SetField(c, 1)) == 0 {
			t.Fatalf("relogin130 ent%v %+v %v", ent, c, err)
		}
	}
	// A receipt is not permission to publish over a later asset revision.
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET revision=revision+1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, found, err = store.ResolveSecondJob(ctx, request); !found || !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("later revision resolved old snapshot", found, err)
	}
	stale := request
	stale.Session.Generation++
	if _, _, err = store.ResolveSecondJob(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale owner resolved", err)
	}
	// Inject failure after all stat/base/revision/roster writes, before receipt.
	if _, err = store.db.ExecContext(ctx, `CREATE FUNCTION spearman_fail_receipt()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.operation_id='spearman-forced-receipt-rollback' THEN RAISE EXCEPTION 'intentional advancement rollback';END IF;RETURN NEW;END$$;CREATE TRIGGER spearman_fail_receipt BEFORE INSERT ON game_job_advancements FOR EACH ROW EXECUTE FUNCTION spearman_fail_receipt()`); err != nil {
		t.Fatal(err)
	}
	failed := request
	failed.Session = b
	failed.OperationID = "spearman-forced-receipt-rollback"
	if _, err = store.AdvanceSecondJob(ctx, failed); err == nil {
		t.Fatal("forced receipt failure committed")
	}
	loaded, err := store.GetCharacter(ctx, b.CharacterID)
	if err != nil || loaded.Job != 100 || loaded.SP != 5 || loaded.Revision != b.Character.Revision || loaded.BaseMaxHP != 500 || loaded.HP != 100 || loaded.MP != 20 {
		t.Fatalf("rollback leaked %+v %v", loaded, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := request
			q.Session = b
			q.OperationID = fmt.Sprintf("spearman-concurrent-operation-%d", i)
			_, err := store.AdvanceSecondJob(ctx, q)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, domain.ErrStaleOwner) && !errors.Is(err, domain.ErrForbidden) {
			t.Fatal(err)
		}
	}
	loaded, err = store.GetCharacter(ctx, b.CharacterID)
	if err != nil || wins != 1 || loaded.Job != 130 || loaded.SP != 6 {
		t.Fatalf("concurrent grants wins%d %+v %v", wins, loaded, err)
	}
}
