package persistence

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
)

func TestChannelTransferFencesOldConnection(t *testing.T) {
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("isolated Go PostgreSQL required")
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
	acc, err := store.Register(ctx, "ch"+suffix, "ChannelTest145")
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
		_, _ = store.db.ExecContext(cleanup, "DELETE FROM ops_runtime_leases WHERE world_id=124 AND instance_id=$1", "channel-"+suffix)
		store.roster.Invalidate(cleanup, acc.ID, 124)
	})
	if err = store.ConfigureAccount(ctx, acc.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := store.BeginLogin(ctx, acc.ID, "channel-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireWorldLease(ctx, 124, "channel-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	chr, err := store.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: acc.ID, WorldID: 124, Name: "Chan" + suffix, Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "channel-create-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: acc.ID, CharacterID: chr.ID, WorldID: 124, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: 124, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	// Unsaved progress survives in the detached in-memory aggregate.
	session.Character.X = 321
	session.Character.Y = -45
	if err = store.BeginChannelTransfer(ctx, session, 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("same channel=%v", err)
	}
	if err = store.BeginChannelTransfer(ctx, session, 2); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCharacter(ctx, session, session.Character); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("source can save during transfer: %v", err)
	}
	if _, err = store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: 10, OperationID: "channel-stale-" + suffix, Reason: "test"}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("source can change assets during transfer: %v", err)
	}
	if _, err = store.FinishChannelTransfer(ctx, session, 3); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("wrong destination: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan domain.CharacterSession, 4)
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, e := store.FinishChannelTransfer(ctx, session, 2)
			if e == nil {
				results <- next
			} else {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	if len(results) != 1 {
		t.Fatalf("successful consumers=%d", len(results))
	}
	for e := range failures {
		if !errors.Is(e, domain.ErrTransferInvalid) {
			t.Fatal(e)
		}
	}
	next := <-results
	if next.ChannelID != 2 || next.Generation != session.Generation+1 || next.Character.X != 321 || next.Character.Y != -45 {
		t.Fatalf("lost cached state: %+v", next)
	}
	// Source cleanup cannot release the new generation.
	if err = store.ReleaseCharacter(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCharacter(ctx, next, next.Character); err != nil {
		t.Fatal(err)
	}
	next.Character.Revision++
	if err = store.BeginChannelTransfer(ctx, next, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, "UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1", acc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinishChannelTransfer(ctx, next, 1); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("expired transfer: %v", err)
	}
	if err = store.ReleaseCharacter(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err = store.BeginLogin(ctx, acc.ID, "channel-relogin-"+suffix); err != nil {
		t.Fatalf("expired transfer blocks login: %v", err)
	}
}
