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
	"maple-go-server/internal/protocol"
)

func firstJobsFixture(t *testing.T, npcID, job int) (context.Context, *Store, []domain.CharacterSession, domain.JobAdvancementRequest) {
	t.Helper()
	ctx, store, actors := inventoryFixture(t)
	root := t.TempDir()
	write := func(name, xml string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(xml), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Minimal copies of the reviewed WZ reward facts, independent of runtime cwd.
	for id, pad := range map[int]int{1402001: 20, 1372005: 15, 1452002: 25, 1482014: 20, 1492014: 16} {
		write(fmt.Sprintf("Character.wz/Weapon/%08d.img.xml", id), fmt.Sprintf(`<imgdir name="%08d.img"><imgdir name="info"><int name="incPAD" value="%d"/><int name="incMAD" value="23"/><int name="tuc" value="7"/></imgdir></imgdir>`, id, pad))
	}
	write("Item.wz/Consume/0206.img.xml", `<imgdir name="0206.img"><imgdir name="02060000"><imgdir name="info"><int name="slotMax" value="2000"/></imgdir></imgdir></imgdir>`)
	write("Item.wz/Consume/0233.img.xml", `<imgdir name="0233.img"><imgdir name="02330006"><imgdir name="info"><int name="slotMax" value="600"/><int name="tradeBlock" value="1"/></imgdir></imgdir></imgdir>`)
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	r := domain.JobAdvancementRequest{Session: actors[0], OperationID: fmt.Sprintf("firstjob-%d-%d", actors[0].CharacterID, npcID), NPCID: npcID, Script: fmt.Sprintf("npc/%d.lua", npcID), TargetJob: job}
	p, err := firstJobPolicyFor(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET level=$1,str=42,dex=11,int_stat=7,luk=6,ap=8,sp=0,hp=50,base_max_hp=182,max_hp=182,mp=10,base_max_mp=104,max_mp=104,map_id=$2 WHERE id=$3`, p.minLevel, p.mapID, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	actors[0].Character, err = store.GetCharacter(ctx, actors[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	r.Session = actors[0]
	return ctx, store, actors, r
}

func TestFirstJobSourcesCommitReplayAndReload(t *testing.T) {
	for _, pair := range [][2]int{{1032001, 200}, {1012100, 300}, {1052001, 100}, {1052001, 200}, {1052001, 300}, {1052001, 400}, {1052001, 500}, {1090000, 500}} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			ctx, s, actors, r := firstJobsFixture(t, pair[0], pair[1])
			actor := actors[0]
			var wg sync.WaitGroup
			results := make(chan domain.JobAdvancementResult, 3)
			for range 3 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					v, err := s.AdvanceFirstJob(ctx, r)
					if err != nil {
						t.Errorf("advance: %v", err)
						return
					}
					results <- v
				}()
			}
			wg.Wait()
			close(results)
			fresh, total := 0, 0
			for v := range results {
				total++
				if !v.Replayed {
					fresh++
				}
			}
			if total != 3 || fresh != 1 {
				t.Fatalf("concurrent replay total=%d commits=%d", total, fresh)
			}
			loaded, err := s.GetCharacter(ctx, actor.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Job != pair[1] || loaded.SP != 1 || loaded.STR+loaded.DEX+loaded.INT+loaded.LUK+loaded.AP != 74 || loaded.HP != loaded.MaxHP || loaded.MP != loaded.MaxMP {
				t.Fatalf("persisted job stats: %+v", loaded)
			}
			if pair[0] == 1090000 {
				if loaded.DEX != 20 || loaded.AP != 42 {
					t.Fatal("post-change pirate reset lost")
				}
			} else if loaded.DEX != 4 || loaded.INT != 4 || loaded.AP != 58 {
				t.Fatal("pre-change reset was not overwritten")
			}
			wantCapacity := 24
			if pair[0] == 1032001 || pair[0] == 1012100 {
				wantCapacity = 28
			}
			if loaded.Capacity(1) != wantCapacity || loaded.Capacity(4) != wantCapacity || loaded.Capacity(2) != 24 {
				t.Fatalf("source-specific expansion changed: %+v", loaded.InventorySlots)
			}
			quantities := map[int]int{}
			stacks := 0
			for _, e := range loaded.Equipment {
				if e.Slot > 0 {
					quantities[e.ItemID]++
				}
			}
			for _, i := range loaded.Items {
				quantities[i.TemplateID] += i.Quantity
				if i.TemplateID == 2330006 {
					stacks++
					if i.Quantity != 600 {
						t.Fatal("bullet grant merged across original calls")
					}
				}
			}
			for _, item := range actor.Character.Items {
				quantities[item.TemplateID] -= item.Quantity
				if quantities[item.TemplateID] == 0 {
					delete(quantities, item.TemplateID)
				}
			}
			switch pair[0] {
			case 1032001:
				if quantities[1372005] != 1 || loaded.BaseMaxHP != 182 || loaded.BaseMaxMP < 204 || loaded.BaseMaxMP > 254 {
					t.Fatalf("mage grant: %+v", loaded)
				}
			case 1012100:
				if quantities[1452002] != 1 || quantities[2060000] != 1000 {
					t.Fatalf("bowman grant: %+v", quantities)
				}
			case 1052001:
				if len(quantities) != 0 {
					t.Fatalf("generic invented gifts: %+v", quantities)
				}
			case 1090000:
				if quantities[1482014] != 1 || quantities[1492014] != 1 || quantities[2330006] != 1800 || stacks != 3 {
					t.Fatalf("pirate grant: %+v stacks=%d", quantities, stacks)
				}
			}
			r.Session.Character = loaded
			changed := r
			changed.TargetJob = 400
			if pair[1] == 400 {
				changed.TargetJob = 500
			}
			if _, err = s.AdvanceFirstJob(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("receipt changed target: %v", err)
			}
			// Read both SQL/Ent authoritative paths and the lean roster projection.
			viaSQL, err := readCharacterSQL(ctx, s.db, actor.CharacterID)
			if err != nil || viaSQL.Job != loaded.Job || viaSQL.AP != loaded.AP || viaSQL.BaseMaxMP != loaded.BaseMaxMP {
				t.Fatalf("SQL roundtrip: %+v %v", viaSQL, err)
			}
			roster, err := s.ListCharacters(ctx, actor.AccountID, actor.Lease.WorldID)
			if err != nil || len(roster) != 1 || roster[0].Job != loaded.Job || roster[0].SP != 1 {
				t.Fatalf("roster roundtrip: %+v %v", roster, err)
			}
			if len(protocol.SetField(loaded, 1)) == 0 {
				t.Fatal("advanced character rejected by field encoder")
			}
			// End ownership, issue a real transfer, and reload the committed job.
			actor.Character = loaded
			if err = s.ReleaseCharacter(ctx, actor); err != nil {
				t.Fatal(err)
			}
			login, err := s.BeginLogin(ctx, actor.AccountID, "firstjob-relogin-"+r.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			tr, err := s.CreateTransfer(ctx, domain.TransferRequest{AccountID: actor.AccountID, CharacterID: actor.CharacterID, WorldID: actor.Lease.WorldID, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			next, err := s.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: actor.Lease.WorldID, ChannelID: 1, ClientIP: "127.0.0.1", Lease: actor.Lease, AllowLegacyLoopback: true})
			if err != nil || next.Character.Job != loaded.Job || next.Character.AP != loaded.AP || next.Character.SP != 1 || len(protocol.SetField(next.Character, 1)) == 0 {
				t.Fatalf("first-job relogin: %+v %v", next, err)
			}
			other, err := s.GetCharacter(ctx, actors[1].CharacterID)
			if err != nil || other.Job != 0 || other.SP != 0 {
				t.Fatalf("other account changed: %+v %v", other, err)
			}
		})
	}
}

func TestFirstJobCapacityFailureRollsBackAndGenericHasNoGrant(t *testing.T) {
	for _, pair := range [][2]int{{1032001, 200}, {1012100, 300}, {1090000, 500}, {1052001, 400}} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			ctx, s, _, r := firstJobsFixture(t, pair[0], pair[1])
			if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_inventory_capacity(character_id,inv_type,capacity) VALUES($1,1,96)`, r.Session.CharacterID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot) SELECT $1,1322005,n FROM generate_series(1,96)n`, r.Session.CharacterID); err != nil {
				t.Fatal(err)
			}
			var err error
			r.Session.Character, err = s.GetCharacter(ctx, r.Session.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			v, err := s.AdvanceFirstJob(ctx, r)
			if pair[0] == 1052001 {
				if err != nil || v.Character.Job != 400 || v.Character.Capacity(1) != 96 || len(v.Changes) != 0 {
					t.Fatalf("generic incorrectly needs reward space: %+v %v", v, err)
				}
				return
			}
			if !errors.Is(err, domain.ErrInsufficient) {
				t.Fatalf("full equipment bag accepted: %v", err)
			}
			loaded, err := s.GetCharacter(ctx, r.Session.CharacterID)
			if err != nil || loaded.Job != 0 || loaded.SP != 0 || loaded.HP != 50 || loaded.BaseMaxHP != 182 || loaded.BaseMaxMP != 104 || loaded.Capacity(4) != 24 || loaded.Revision != r.Session.Character.Revision {
				t.Fatalf("failed job partially committed: %+v %v", loaded, err)
			}
		})
	}
}

func TestFirstJobLateRewardFailureAndContextIsolation(t *testing.T) {
	ctx, s, actors, r := firstJobsFixture(t, 1090000, 500)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) SELECT $1,2,n,2000000,1 FROM generate_series(2,22)n`, r.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	r.Session.Character, err = s.GetCharacter(ctx, r.Session.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceFirstJob(ctx, r); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("third bullet stack overflow accepted: %v", err)
	}
	loaded, err := s.GetCharacter(ctx, r.Session.CharacterID)
	if err != nil || loaded.Job != 0 || loaded.SP != 0 || len(loaded.Equipment) != len(r.Session.Character.Equipment) || len(loaded.Items) != 22 {
		t.Fatalf("partial pirate grant: %+v %v", loaded, err)
	}
	for name, edit := range map[string]func(*domain.JobAdvancementRequest){
		"wrong-world":      func(v *domain.JobAdvancementRequest) { v.Session.Lease.WorldID++ },
		"wrong-account":    func(v *domain.JobAdvancementRequest) { v.Session.AccountID = actors[1].AccountID },
		"wrong-map":        func(v *domain.JobAdvancementRequest) { v.Session.Character.MapID = 10000 },
		"stale-generation": func(v *domain.JobAdvancementRequest) { v.Session.Generation++ },
		"wrong-source":     func(v *domain.JobAdvancementRequest) { v.Script = "npc/1052001.lua" },
		"subclass":         func(v *domain.JobAdvancementRequest) { v.TargetJob = 501 },
	} {
		t.Run(name, func(t *testing.T) {
			v := r
			edit(&v)
			if _, err := s.AdvanceFirstJob(ctx, v); err == nil {
				t.Fatal("invalid context advanced")
			}
		})
	}
	var receipts int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM game_job_advancements WHERE character_id=$1`, r.Session.CharacterID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed transaction wrote receipt: %d %v", receipts, err)
	}
}
