package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
)

func TestItemUseAtomicReplayAndReload(t *testing.T) {
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("set MAPLEGO_TEST_INTEGRATION=1 against the isolated Go database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 6})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = migrate.Apply(ctx, store.db); err != nil {
		t.Fatal(err)
	}
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := store.Register(ctx, "it"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster", "game_characters", "auth_accounts"} {
			column := "account_id"
			if table == "auth_accounts" {
				column = "id"
			}
			if _, e := store.db.ExecContext(cleanup, "DELETE FROM "+table+" WHERE "+column+"=$1", acc.ID); e != nil {
				t.Errorf("cleanup %s: %v", table, e)
			}
		}
		_, _ = store.db.ExecContext(cleanup, "DELETE FROM ops_runtime_leases WHERE world_id=125 AND instance_id=$1", "items-world-"+suffix)
		store.roster.Invalidate(cleanup, acc.ID, 125)
	})
	if err = store.ConfigureAccount(ctx, acc.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := store.BeginLogin(ctx, acc.ID, "items-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireWorldLease(ctx, 125, "items-world-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	chr, err := store.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: acc.ID, WorldID: 125, Name: "Item" + suffix, Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "items-create-" + suffix + "-operation"})
	if err != nil {
		t.Fatal(err)
	}
	enter := func(login domain.LoginSession) domain.CharacterSession {
		t.Helper()
		tr, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: acc.ID, CharacterID: chr.ID, WorldID: 125, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: 125, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	// This fixture needs intrinsic 100HP so a 50HP potion heals 5->55.
	// The progress checkpoint may not invent an intrinsic stat increase.
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET base_max_hp=100,max_hp=100 WHERE id=$1`, chr.ID); err != nil {
		t.Fatal(err)
	}
	session := enter(login)
	// Seed damage through the regular fenced checkpoint API, not an unchecked UPDATE.
	session.Character.HP = 5
	if err = store.SaveCharacter(ctx, session, session.Character); err != nil {
		t.Fatal(err)
	}
	session.Character.Revision++
	request := domain.UseItemRequest{Session: session, ClientTick: ^uint32(0), Slot: 1, TemplateID: 2000000}
	first, err := store.UseItem(ctx, request)
	if err != nil || first.HP != 55 || len(first.Items) != 1 || first.Items[0].Quantity != 4 {
		t.Fatalf("first use: %+v error=%v", first, err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, e := store.UseItem(ctx, request)
			if e != nil || value.HP != 55 || len(value.Items) != 1 || value.Items[0].Quantity != 4 || value.Revision != first.Revision {
				t.Errorf("concurrent replay: %+v error=%v", value, e)
			}
		}()
	}
	wg.Wait()
	session.Character = first
	for _, tc := range []struct {
		name string
		edit func(*domain.UseItemRequest)
		want error
	}{
		{"changed replay", func(r *domain.UseItemRequest) { r.Slot = 2 }, domain.ErrForbidden},
		// An existing operation key binds the original template before a new
		// effect can be considered. A fresh key still requires an owned item.
		{"changed template replay", func(r *domain.UseItemRequest) { r.TemplateID = 2999999 }, domain.ErrForbidden},
		{"unowned unsupported item", func(r *domain.UseItemRequest) { r.ClientTick = 9; r.TemplateID = 2999999 }, domain.ErrNotFound},
		{"missing slot", func(r *domain.UseItemRequest) { r.ClientTick = 9; r.Slot = 2 }, domain.ErrNotFound},
		{"stale revision", func(r *domain.UseItemRequest) { r.ClientTick = 9; r.Session.Character.Revision-- }, domain.ErrStaleOwner},
		{"stale generation", func(r *domain.UseItemRequest) { r.Session.Generation++ }, domain.ErrStaleOwner},
		{"stale epoch", func(r *domain.UseItemRequest) { r.Session.Lease.Epoch++ }, domain.ErrStaleOwner},
		{"dead character", func(r *domain.UseItemRequest) { r.ClientTick = 9; r.Session.Character.HP = 0 }, domain.ErrInvalidInput},
		{"forged blocked map snapshot", func(r *domain.UseItemRequest) { r.ClientTick = 9; r.Session.Character.MapID = 749040100 }, domain.ErrStaleOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request
			r.Session = session
			tc.edit(&r)
			if _, e := store.UseItem(ctx, r); !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
		})
	}
	t.Run("owned unsupported item", func(t *testing.T) {
		var itemID int64
		if err := store.db.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,2,2999999,1) RETURNING id`, chr.ID).Scan(&itemID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := store.db.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1 AND character_id=$2`, itemID, chr.ID); err != nil {
				t.Error("unsupported fixture cleanup", err)
			}
		})
		q := domain.UseItemRequest{Session: session, ClientTick: 9, Slot: 2, TemplateID: 2999999}
		if _, err := store.UseItem(ctx, q); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("owned unsupported recovery: got %v want %v", err, domain.ErrInvalidInput)
		}
	})
	t.Run("persisted blocked map", func(t *testing.T) {
		// A local forged map is stale; this case puts both the authoritative
		// row and the request in the blocked map to exercise its actual policy.
		if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET map_id=749040100 WHERE id=$1`, chr.ID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET map_id=$1 WHERE id=$2`, session.Character.MapID, chr.ID); err != nil {
				t.Error("blocked map fixture cleanup", err)
			}
		})
		q := domain.UseItemRequest{Session: session, ClientTick: 9, Slot: 1, TemplateID: 2000000}
		q.Session.Character.MapID = 749040100
		if _, err := store.UseItem(ctx, q); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("authoritative blocked map: got %v want %v", err, domain.ErrInvalidInput)
		}
	})
	rejectedState, err := store.GetCharacter(ctx, chr.ID)
	var rejectedReceipts int
	if err != nil || rejectedState.HP != first.HP || rejectedState.MP != first.MP || rejectedState.Revision != first.Revision || len(rejectedState.Items) != 1 || rejectedState.Items[0].Quantity != 4 || rejectedState.MapID != session.Character.MapID {
		t.Fatalf("rejected uses changed assets: %+v error=%v", rejectedState, err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_uses WHERE character_id=$1`, chr.ID).Scan(&rejectedReceipts); err != nil || rejectedReceipts != 1 {
		t.Fatalf("rejected uses wrote receipts: %d error=%v", rejectedReceipts, err)
	}
	// Fail after inventory/HP writes to prove both roll back with the receipt.
	fn := "item_use_failure_" + suffix
	if _, err = store.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test item receipt failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_item_uses FOR EACH ROW WHEN (NEW.character_id=%d AND NEW.client_tick=99) EXECUTE FUNCTION %s()`, fn, fn, chr.ID, fn)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, e := store.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_item_uses; DROP FUNCTION IF EXISTS %s()", fn, fn))
		if e != nil {
			t.Errorf("trigger cleanup: %v", e)
		}
	})
	failed := domain.UseItemRequest{Session: session, ClientTick: 99, Slot: 1, TemplateID: 2000000}
	if _, err = store.UseItem(ctx, failed); err == nil {
		t.Fatal("injected receipt failure was ignored")
	}
	unchanged, err := store.GetCharacter(ctx, chr.ID)
	if err != nil || unchanged.HP != 55 || unchanged.Revision != first.Revision || unchanged.Items[0].Quantity != 4 {
		t.Fatalf("failed transaction changed character: %+v error=%v", unchanged, err)
	}
	for tick := uint32(1); tick <= 4; tick++ {
		value, e := store.UseItem(ctx, domain.UseItemRequest{Session: session, ClientTick: tick, Slot: 1, TemplateID: 2000000})
		if e != nil || value.HP != 100 {
			t.Fatalf("use %d: HP=%d error=%v", tick, value.HP, e)
		}
		session.Character = value
	}
	if len(session.Character.Items) != 0 {
		t.Fatal("last use did not remove stack")
	}
	replayed, err := store.UseItem(ctx, request)
	if err != nil || replayed.HP != 100 || len(replayed.Items) != 0 {
		t.Fatalf("old replay returned stale state: %+v error=%v", replayed, err)
	}
	if _, err = store.UseItem(ctx, domain.UseItemRequest{Session: session, ClientTick: 5, Slot: 1, TemplateID: 2000000}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty slot use: %v", err)
	}
	var receipts int
	if err = store.db.QueryRowContext(ctx, "SELECT count(*) FROM asset_item_uses WHERE character_id=$1", chr.ID).Scan(&receipts); err != nil || receipts != 5 {
		t.Fatalf("receipts=%d error=%v", receipts, err)
	}
	if err = store.ReleaseCharacter(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UseItem(ctx, request); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("released session replay: %v", err)
	}
	login, err = store.BeginLogin(ctx, acc.ID, "items-relogin-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := enter(login)
	if reloaded.Character.HP != 100 || len(reloaded.Character.Items) != 0 {
		t.Fatalf("reload lost inventory/HP: %+v", reloaded.Character)
	}
	if err = store.ReleaseCharacter(ctx, reloaded); err != nil {
		t.Fatal(err)
	}
}

func TestQuestAppleAndMPRecoveryUse(t *testing.T) {
	ctx, store, sessions := inventoryFixture(t)
	session := sessions[0]
	session.Character.HP = 20
	session.Character.MP = 1
	if err := store.SaveCharacter(ctx, session, session.Character); err != nil {
		t.Fatal(err)
	}
	session.Character.Revision++
	grant, err := store.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: session, OperationID: "recovery-quest-" + fmt.Sprint(session.CharacterID), Items: []domain.ScriptItemDelta{{TemplateID: 2010007, Quantity: 1}, {TemplateID: 2010009, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	session.Character = grant.Character
	for i, id := range []int{2010007, 2010009} {
		slot := 0
		for _, item := range session.Character.Items {
			if item.TemplateID == id {
				slot = item.Slot
			}
		}
		request := domain.UseItemRequest{Session: session, Slot: slot, TemplateID: id, ClientTick: uint32(800 + i)}
		value, err := store.UseItem(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if value.HP != 50 || (i == 1 && value.MP != 5) {
			t.Fatalf("wrong recovery HP%d MP%d", value.HP, value.MP)
		}
		session.Character = value
		replay, err := store.UseItem(ctx, request)
		if err != nil || replay.Revision != value.Revision {
			t.Fatalf("recovery replay=%+v %v", replay, err)
		}
	}
	for _, item := range session.Character.Items {
		if item.TemplateID == 2010007 || item.TemplateID == 2010009 {
			t.Fatal("used stack retained")
		}
	}
}
