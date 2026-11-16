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

func inventoryFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("set MAPLEGO_TEST_INTEGRATION=1 against isolated Go DB")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = migrate.Apply(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireWorldLease(ctx, 124, "inventory-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.ReleaseWorldLease(context.Background(), lease)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM ops_runtime_leases WHERE world_id=124 AND instance_id=$1`, lease.InstanceID)
	})
	sessions := make([]domain.CharacterSession, 0, 2)
	for n := 0; n < 2; n++ {
		account, err := s.Register(ctx, fmt.Sprintf("iv%d%s", n, suffix), "TestPassword145")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_item_drops WHERE owner_id IN(SELECT id FROM game_characters WHERE account_id=$1) OR picked_up_by IN(SELECT id FROM game_characters WHERE account_id=$1)`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_trade_settlements WHERE first_character_id IN(SELECT id FROM game_characters WHERE account_id=$1) OR second_character_id IN(SELECT id FROM game_characters WHERE account_id=$1)`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_meso_ledger WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_equipment_metadata WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_moves WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_gifts WHERE sender_account_id=$1 OR recipient_character_id IN(SELECT id FROM game_characters WHERE account_id=$1)`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_feature_operations WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_coupons WHERE account_id=$1 OR redeemed_account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_orders WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_ledger WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_items WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_cash_wallets WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM ops_service_transfers WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_storage_operations WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_shop_operations WHERE account_id=$1`, account.ID)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_storages WHERE account_id=$1`, account.ID)
			for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster", "game_characters", "auth_accounts"} {
				column := "account_id"
				if table == "auth_accounts" {
					column = "id"
				}
				if _, e := s.db.ExecContext(cleanup, "DELETE FROM "+table+" WHERE "+column+"=$1", account.ID); e != nil {
					t.Errorf("cleanup %s: %v", table, e)
				}
			}
		})
		if err = s.ConfigureAccount(ctx, account.ID, 0, "145145"); err != nil {
			t.Fatal(err)
		}
		login, err := s.BeginLogin(ctx, account.ID, "inventory-login-"+suffix+fmt.Sprint(n))
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: account.ID, WorldID: 124, Name: fmt.Sprintf("IV%d%s", n, suffix), Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: fmt.Sprintf("inventory-create-%s-%d", suffix, n)})
		if err != nil {
			t.Fatal(err)
		}
		tr, err := s.CreateTransfer(ctx, domain.TransferRequest{AccountID: account.ID, CharacterID: c.ID, WorldID: 124, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		session, err := s.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: 124, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	return ctx, s, sessions
}

func equipmentAt(c domain.Character, slot int) domain.Equipment {
	for _, e := range c.Equipment {
		if e.Slot == slot {
			return e
		}
	}
	return domain.Equipment{}
}

func TestInventoryEquipmentCycleAndRollback(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	session := sessions[0]
	weapon := equipmentAt(session.Character, -11)
	r := domain.InventoryMoveRequest{Session: session, ClientTick: 1, InvType: 1, Source: -11, Destination: 1, Quantity: -1}
	result, err := s.MoveInventory(ctx, r)
	if err != nil || equipmentAt(result.Character, 1).ID != weapon.ID {
		t.Fatalf("unequip=%+v err=%v", result, err)
	}
	replay, err := s.MoveInventory(ctx, r)
	if err != nil || !replay.Replayed || replay.Character.Revision != result.Character.Revision || len(replay.Changes) != 0 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	r.Source = -7
	if _, err = s.MoveInventory(ctx, r); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay=%v", err)
	}
	session.Character = result.Character
	bad := domain.InventoryMoveRequest{Session: session, ClientTick: 2, InvType: 1, Source: 1, Destination: -5, Quantity: -1}
	if _, err = s.MoveInventory(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid equipment slot=%v", err)
	}
	bad.Destination = -11
	bad.Session.Generation++
	if _, err = s.MoveInventory(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("invalid owner=%v", err)
	}
	// Give the test weapon a HP modifier while it is in the bag, then exercise
	// real equip/unequip derived MaxHP and clamp semantics.
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_equipment SET hp=10 WHERE id=$1`, weapon.ID); err != nil {
		t.Fatal(err)
	}
	apply := func(tick uint32, src, dst int) {
		t.Helper()
		result, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: session, ClientTick: tick, InvType: 1, Source: src, Destination: dst, Quantity: -1})
		if err != nil {
			t.Fatal(err)
		}
		session.Character = result.Character
	}
	apply(2, 1, -11)
	if session.Character.MaxHP != 60 {
		t.Fatalf("equip maxHP=%d", session.Character.MaxHP)
	}
	apply(3, -11, 1)
	if session.Character.MaxHP != 50 {
		t.Fatalf("unequip maxHP=%d", session.Character.MaxHP)
	}
	apply(4, -7, 2)
	shoe := equipmentAt(session.Character, 2)
	apply(5, 1, 2)
	if equipmentAt(session.Character, 2).ID != weapon.ID || equipmentAt(session.Character, 1).ID != shoe.ID {
		t.Fatal("bag swap lost item identity")
	}
	apply(6, 2, -11)
	apply(7, 1, -7)
	before := session.Character
	fn := fmt.Sprintf("inventory_fail_%d", session.CharacterID)
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test inventory receipt failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_inventory_commands FOR EACH ROW WHEN (NEW.character_id=%d AND NEW.client_tick=99) EXECUTE FUNCTION %s()`, fn, fn, session.CharacterID, fn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_inventory_commands; DROP FUNCTION IF EXISTS %s()", fn, fn))
	})
	if _, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: session, ClientTick: 99, InvType: 1, Source: -11, Destination: 1, Quantity: -1}); err == nil {
		t.Fatal("expected injected receipt failure")
	}
	current, err := s.GetCharacter(ctx, session.CharacterID)
	if err != nil || current.Revision != before.Revision || equipmentAt(current, -11).ID != weapon.ID || current.MaxHP != 60 {
		t.Fatalf("rollback=%+v err=%v", current, err)
	}
	if err = s.SaveCharacter(ctx, session, before); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryDropConcurrentPickupConservesAssets(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	result, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 1, InvType: 2, Source: 1, Destination: 2, Quantity: -1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = result.Character
	result, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 2, InvType: 2, Source: 2, Destination: 0, Quantity: 2})
	if err != nil || result.Drop == nil || result.Drop.Quantity != 2 || result.Drop.ObjectID < 1000000000 || !result.Drop.PlayerDrop {
		t.Fatalf("drop=%+v err=%v", result, err)
	}
	a.Character = result.Character
	drop := result.Drop
	far := b
	far.Character.X += 1000
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: far, ObjectID: drop.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("far pickup=%v", err)
	}
	wrong := b
	wrong.ChannelID = 2
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: wrong, ObjectID: drop.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-channel pickup=%v", err)
	}
	var wg sync.WaitGroup
	type outcome struct {
		session domain.CharacterSession
		result  domain.InventoryResult
		err     error
	}
	outcomes := make(chan outcome, 2)
	for _, session := range []domain.CharacterSession{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.PickupItem(ctx, domain.ItemPickupRequest{Session: session, ObjectID: drop.ObjectID})
			outcomes <- outcome{session, r, e}
		}()
	}
	wg.Wait()
	close(outcomes)
	winners := 0
	var winner domain.CharacterSession
	for outcome := range outcomes {
		if outcome.err == nil {
			winners++
			winner = outcome.session
			winner.Character = outcome.result.Character
		} else if !errors.Is(outcome.err, domain.ErrNotFound) {
			t.Fatalf("unexpected pickup=%v", outcome.err)
		}
	}
	if winners != 1 {
		t.Fatalf("pickup winners=%d", winners)
	}
	replay, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: winner, ObjectID: drop.ObjectID})
	if err != nil || !replay.Replayed {
		t.Fatalf("pickup replay=%+v err=%v", replay, err)
	}
	quantity := 0
	for _, session := range []domain.CharacterSession{a, b} {
		c, e := s.GetCharacter(ctx, session.CharacterID)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range c.Items {
			quantity += item.Quantity
		}
	}
	if quantity != 10 {
		t.Fatalf("total potion quantity=%d want10", quantity)
	}
	drops, err := s.ListItemDrops(ctx, a.Lease.WorldID, a.ChannelID, a.Character.MapID)
	if err != nil || len(drops) != 0 {
		t.Fatalf("consumed drop remains %+v %v", drops, err)
	}
	var owner int64
	if err = s.db.QueryRowContext(ctx, `SELECT picked_up_by FROM asset_item_drops WHERE object_id=$1`, drop.ObjectID).Scan(&owner); err != nil || owner != winner.CharacterID {
		t.Fatalf("durable pickup owner=%d %v", owner, err)
	}
}
