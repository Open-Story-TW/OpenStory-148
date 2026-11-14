package persistence

import (
	"context"
	"errors"
	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMesoLedgerIdempotentAndConcurrent(t *testing.T) {
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("set MAPLEGO_TEST_INTEGRATION=1 against the isolated Go database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 6}
	store, err := Open(ctx, cfg)
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
	acc, err := store.Register(ctx, "ms"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM asset_meso_ledger WHERE account_id=$1", acc.ID)
		for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster"} {
			_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM "+table+" WHERE account_id=$1", acc.ID)
		}
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM asset_equipment WHERE character_id IN(SELECT id FROM game_characters WHERE account_id=$1)", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM game_characters WHERE account_id=$1", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM auth_accounts WHERE id=$1", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM ops_runtime_leases WHERE world_id=127")
	})
	if err = store.ConfigureAccount(ctx, acc.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := store.BeginLogin(ctx, acc.ID, "meso-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireWorldLease(ctx, 127, "meso-world-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.ReleaseWorldLease(context.Background(), lease) })
	char, err := store.CreateCharacter(ctx, domain.CreateCharacterRequest{
		LoginSession: login, Lease: lease, AccountID: acc.ID, WorldID: 127, Name: "Meso" + suffix,
		Gender: 0, Skin: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005,
		OperationID: "meso-create-" + suffix + "-abcdefgh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if char.Mesos != 1000 {
		t.Fatalf("starter mesos=%d", char.Mesos)
	}
	transfer, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: acc.ID, CharacterID: char.ID, WorldID: 127, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: transfer.ClientHandle, WorldID: 127, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	op := "meso-drop-op-" + suffix + "-xxxx"
	first, err := store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: -100, OperationID: op, Reason: "player_drop"})
	if err != nil || first.Mesos != 900 {
		t.Fatalf("drop: mesos=%d err=%v", first.Mesos, err)
	}
	replay, err := store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: -100, OperationID: op, Reason: "player_drop"})
	if err != nil || replay.Mesos != 900 {
		t.Fatalf("idempotent replay: mesos=%d err=%v", replay.Mesos, err)
	}
	session.Character = first
	if _, err = store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: -5000, OperationID: "meso-over-" + suffix + "-xxxx", Reason: "player_drop"}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("expected insufficient, got %v", err)
	}
	pickupOp := "meso-pick-op-" + suffix + "-xxxx"
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: 100, OperationID: pickupOp, Reason: "meso_pickup"})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for e := range results {
		if e == nil {
			ok++
		} else if !errors.Is(e, domain.ErrForbidden) && e != nil {
			// concurrent losers may see unique violation converted? we return prior on conflict via select first
			t.Fatalf("unexpected: %v", e)
		}
	}
	// All 5 should succeed idempotently (same operation_id).
	if ok != 5 {
		t.Fatalf("idempotent concurrent successes=%d", ok)
	}
	loaded, err := store.GetCharacter(ctx, char.ID)
	if err != nil || loaded.Mesos != 1000 {
		t.Fatalf("final mesos=%d err=%v", loaded.Mesos, err)
	}
}
