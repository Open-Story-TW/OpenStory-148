package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func warriorFixture(t *testing.T) (context.Context, *Store, domain.CharacterSession) {
	t.Helper()
	ctx, store, actors := inventoryFixture(t)
	root := t.TempDir()
	dir := filepath.Join(root, "Character.wz", "Weapon")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01402001.img.xml"), []byte(`<imgdir name="01402001.img"><imgdir name="info"><int name="incPAD" value="20"/><int name="tuc" value="7"/><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	actor := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET level=10,str=57,hp=50,base_max_hp=182,max_hp=182,mp=10,base_max_mp=104,max_mp=104,map_id=102000003 WHERE id=$1`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store, actor
}

func TestWarriorAdvancementAtomicReplayAndPersistence(t *testing.T) {
	ctx, store, actor := warriorFixture(t)
	// A full original bag must use newly granted capacity, not reject the
	// reward or overwrite one of the existing twenty-four equipment slots.
	if _, err := store.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot) SELECT $1,1322005,n FROM generate_series(1,24) n`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req := domain.JobAdvancementRequest{Session: actor, OperationID: fmt.Sprintf("warrior-advance-%d", actor.CharacterID), NPCID: 1022000, Script: "npc/1022000.lua"}
	var wg sync.WaitGroup
	results := make(chan domain.JobAdvancementResult, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := store.AdvanceWarrior(ctx, req)
			if err != nil {
				t.Errorf("advance: %v", err)
				return
			}
			results <- value
		}()
	}
	wg.Wait()
	close(results)
	committed, count := 0, 0
	for result := range results {
		count++
		if !result.Replayed {
			committed++
			if len(result.Changes) != 1 || result.Changes[0].Equipment.ItemID != 1402001 || result.Changes[0].Slot != 25 {
				t.Fatalf("weapon reward: %+v", result)
			}
		}
	}
	if count != 3 || committed != 1 {
		t.Fatalf("repeated advancement count=%d new=%d", count, committed)
	}
	loaded, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || loaded.Job != 100 || loaded.STR != 4 || loaded.DEX != 4 || loaded.INT != 4 || loaded.LUK != 4 || loaded.AP != 54 || loaded.SP != 1 || loaded.MaxHP < 382 || loaded.MaxHP > 432 || loaded.HP != loaded.MaxHP || loaded.MP != 104 || loaded.Capacity(1) != 28 || loaded.Capacity(4) != 28 || loaded.Capacity(2) != 24 {
		t.Fatalf("persisted advancement: %+v %v", loaded, err)
	}
	weapons := 0
	for _, item := range loaded.Equipment {
		if item.ItemID == 1402001 {
			weapons++
		}
	}
	if weapons != 1 {
		t.Fatalf("reward weapon duplicated: %d", weapons)
	}
	actor.Character = loaded
	req.Session = actor
	req.OperationID += "-different"
	if _, err := store.AdvanceWarrior(ctx, req); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("second invocation advanced twice: %v", err)
	}
}

func TestWarriorAdvancementRewardFailureRollsBackAllChanges(t *testing.T) {
	ctx, store, actor := warriorFixture(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO asset_inventory_capacity(character_id,inv_type,capacity) VALUES($1,1,96)`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot) SELECT $1,1322005,n FROM generate_series(1,96) n`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req := domain.JobAdvancementRequest{Session: actor, OperationID: fmt.Sprintf("warrior-full-bag-%d", actor.CharacterID), NPCID: 1022000, Script: "npc/1022000.lua"}
	if _, err = store.AdvanceWarrior(ctx, req); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full bag accepted: %v", err)
	}
	loaded, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || loaded.Job != 0 || loaded.STR != 57 || loaded.AP != 0 || loaded.HP != 50 || loaded.MaxHP != 182 || loaded.SP != 0 || loaded.Capacity(4) != 24 || loaded.Revision != actor.Character.Revision {
		t.Fatalf("failed reward partially advanced: %+v %v", loaded, err)
	}
	var receipts int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM game_job_advancements WHERE character_id=$1`, actor.CharacterID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed advancement receipt=%d %v", receipts, err)
	}
}

func TestWarriorAdvancementRejectsForgedContextAndStaleOwner(t *testing.T) {
	ctx, store, actor := warriorFixture(t)
	base := domain.JobAdvancementRequest{Session: actor, OperationID: fmt.Sprintf("warrior-reject-%d", actor.CharacterID), NPCID: 1022000, Script: "npc/1022000.lua"}
	for name, modify := range map[string]func(*domain.JobAdvancementRequest){
		"npc":    func(r *domain.JobAdvancementRequest) { r.NPCID++ },
		"source": func(r *domain.JobAdvancementRequest) { r.Script = "npc/2100.lua" },
		"map":    func(r *domain.JobAdvancementRequest) { r.Session.Character.MapID = 10000 },
		"owner":  func(r *domain.JobAdvancementRequest) { r.Session.Generation++ },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			modify(&r)
			if _, err := store.AdvanceWarrior(ctx, r); err == nil {
				t.Fatal("invalid context accepted")
			}
		})
	}
}
