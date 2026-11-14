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

func TestCombatRewardLedgerAndBeginnerLevelUp(t *testing.T) {
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("requires isolated Go PostgreSQL/Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 6})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = migrate.Apply(ctx, store.db); err != nil {
		t.Fatal(err)
	}
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.Register(ctx, "cb"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster", "game_characters"} {
			if _, e := store.db.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE account_id=$1", account.ID); e != nil {
				t.Errorf("cleanup%s: %v", table, e)
			}
		}
		_, _ = store.db.ExecContext(context.Background(), "DELETE FROM auth_accounts WHERE id=$1", account.ID)
		_, _ = store.db.ExecContext(context.Background(), "DELETE FROM ops_runtime_leases WHERE world_id=124 AND instance_id=$1", "combat-"+suffix)
	}()
	if err = store.ConfigureAccount(ctx, account.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := store.BeginLogin(ctx, account.ID, "combat-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireWorldLease(ctx, 124, "combat-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	chr, err := store.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: account.ID, WorldID: 124, Name: "Comb" + suffix, Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "combat-create-" + suffix + "-operation"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: account.ID, CharacterID: chr.ID, WorldID: 124, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: 124, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	session.Character.EXP = 14
	if err = store.SaveCharacter(ctx, session, session.Character); err != nil {
		t.Fatal(err)
	}
	session.Character.Revision++
	op := "combat-kill-" + suffix + "-spawn-one"
	value, err := store.AwardCombat(ctx, session, op, 2)
	if err != nil {
		t.Fatal(err)
	}
	if value.Level != 2 || value.EXP != 1 || value.STR != chr.STR+5 || value.AP != 0 || value.MaxHP < chr.MaxHP+12 || value.MaxHP > chr.MaxHP+16 || value.HP != value.MaxHP || value.MP != value.MaxMP {
		t.Fatalf("bad level-up: %+v", value)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replayed, e := store.AwardCombat(ctx, session, op, 2)
			if e != nil || replayed.Revision != value.Revision || replayed.EXP != 1 || replayed.Level != 2 {
				t.Errorf("replay: %+v %v", replayed, e)
			}
		}()
	}
	wg.Wait()
	if _, err = store.AwardCombat(ctx, session, op, 200); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed reward accepted: %v", err)
	}
	if _, err = store.AwardCombat(ctx, session, op+"new", 2); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale owner accepted: %v", err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, "SELECT count(*) FROM asset_combat_rewards WHERE character_id=$1", chr.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger=%d %v", count, err)
	}
	loaded, err := store.GetCharacter(ctx, chr.ID)
	if err != nil || loaded.Level != 2 || loaded.EXP != 1 || loaded.MaxHP != value.MaxHP {
		t.Fatalf("reload: %+v %v", loaded, err)
	}
}
