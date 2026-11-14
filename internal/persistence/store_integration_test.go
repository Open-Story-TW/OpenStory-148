package persistence

import (
	"context"
	"database/sql"
	"errors"
	"maple-go-server/ent"
	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresEntRedisOwnershipFlow(t *testing.T) {
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
	if err = store.CachePing(ctx); err != nil {
		t.Fatal("Redis is required for the cache-hit integration case")
	}
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := store.Register(ctx, "st"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster"} {
			_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM "+table+" WHERE account_id=$1", acc.ID)
		}
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM asset_equipment WHERE character_id IN(SELECT id FROM game_characters WHERE account_id=$1)", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM game_characters WHERE account_id=$1", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM auth_accounts WHERE id=$1", acc.ID)
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM ops_runtime_leases WHERE world_id=126 AND instance_id IN($1,$2)", "test-"+suffix, "test-new-"+suffix)
		store.roster.Invalidate(cleanupCtx, acc.ID, 126)
	})
	if _, err = store.Authenticate(ctx, acc.Name, "wrong"); !errors.Is(err, domain.ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if err = store.ConfigureAccount(ctx, acc.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyPIN(ctx, acc.ID, "145145"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireWorldLease(ctx, 126, "test-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	login, err := store.BeginLogin(ctx, acc.ID, "session-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.BeginLogin(ctx, acc.ID, "second-"+suffix); !errors.Is(err, domain.ErrAlreadyOnline) {
		t.Fatalf("double login allowed: %v", err)
	}
	if err = store.TouchLogin(ctx, login); err != nil {
		t.Fatal(err)
	}
	req := domain.CreateCharacterRequest{Lease: lease, LoginSession: login, AccountID: acc.ID, WorldID: 126, Name: "Go" + suffix, Gender: 0, Face: 20100, Hair: 30000, OperationID: "create-" + suffix}
	var remaining float64
	if err = store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM expires_at-clock_timestamp()) FROM ops_account_sessions WHERE account_id=$1`, acc.ID).Scan(&remaining); err != nil || remaining < 300 {
		t.Fatalf("login idle lifetime is not six minutes: seconds=%f err=%v", remaining, err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1 AND session_id=$2`, acc.ID, login.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = store.TouchLogin(ctx, login); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("expired login renewed: %v", err)
	}
	if _, err = store.CreateCharacter(ctx, req); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("expired login created character: %v", err)
	}
	login, err = store.BeginLogin(ctx, acc.ID, "fresh-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	req.LoginSession = login
	char, err := store.CreateCharacter(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if char.GM != 0 || char.Level != 1 || len(char.Equipment) != 4 {
		t.Fatalf("invalid ordinary-player defaults: %+v", char)
	}
	seen := map[int64]bool{}
	for _, item := range char.Equipment {
		if item.ID <= 0 || seen[item.ID] {
			t.Fatal("equipment IDs are not positive/distinct")
		}
		seen[item.ID] = true
	}
	same, err := store.CreateCharacter(ctx, req)
	if err != nil || same.ID != char.ID {
		t.Fatalf("idempotent creation failed: %v", err)
	}
	req.OperationID = "duplicate-" + suffix
	if _, err = store.CreateCharacter(ctx, req); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	before, _ := store.Stats()
	roster, err := store.ListCharacters(ctx, acc.ID, 126)
	if err != nil || len(roster) != 1 {
		t.Fatalf("roster: %v", err)
	}
	after, _ := store.Stats()
	if after-before != 1 {
		t.Fatalf("cold roster reads = %d, expected 1", after-before)
	}
	_, err = store.ListCharacters(ctx, acc.ID, 126)
	if err != nil {
		t.Fatal(err)
	}
	final, hits := store.Stats()
	if final != after || hits < 1 {
		t.Fatal("warm roster did not use Redis")
	}
	transfer, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: acc.ID, CharacterID: char.ID, WorldID: 126, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if transfer.ClientHandle <= 0 || transfer.Token == "" {
		t.Fatal("missing opaque transfer proof")
	}
	if err = store.EndLogin(ctx, login); err != nil {
		t.Fatal(err)
	} // TCP closes after redirect; pending transfer must survive.
	claim := domain.TransferClaim{ClientHandle: transfer.ClientHandle, WorldID: 126, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true}
	noProof := claim
	noProof.AllowLegacyLoopback = false
	if _, err = store.ConsumeTransfer(ctx, noProof); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatal("proofless non-development transfer accepted")
	}
	// Fault injection occurs after the SQL state changes but before commit.
	var failLoad atomic.Bool
	failLoad.Store(true)
	loadFailure := errors.New("injected character equipment load failure")
	store.client.Equipment.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(queryCtx context.Context, q ent.Query) (ent.Value, error) {
			if failLoad.Load() {
				return nil, loadFailure
			}
			return next.Query(queryCtx, q)
		})
	}))
	if _, err = store.ConsumeTransfer(ctx, claim); !errors.Is(err, loadFailure) {
		t.Fatalf("expected load fault: %v", err)
	}
	var pendingState string
	var consumed sql.NullTime
	var owners int
	if err = store.db.QueryRowContext(ctx, `SELECT s.state,t.consumed_at,(SELECT count(*) FROM ops_character_ownership WHERE character_id=$2) FROM ops_account_sessions s JOIN ops_transfers t ON t.account_id=s.account_id WHERE s.account_id=$1 AND t.id=$3`, acc.ID, char.ID, transfer.ID).Scan(&pendingState, &consumed, &owners); err != nil {
		t.Fatal(err)
	}
	if pendingState != "transfer" || consumed.Valid || owners != 0 {
		t.Fatal("failed full-character load committed orphaned ownership")
	}
	failLoad.Store(false)
	type claimResult struct {
		session domain.CharacterSession
		err     error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 5)
	for i := 0; i < 5; i++ {
		go func() {
			<-start
			session, err := store.ConsumeTransfer(ctx, claim)
			results <- claimResult{session, err}
		}()
	}
	close(start)
	var session domain.CharacterSession
	successes := 0
	for i := 0; i < 5; i++ {
		result := <-results
		if result.err == nil {
			successes++
			session = result.session
		} else if !errors.Is(result.err, domain.ErrTransferInvalid) {
			t.Fatalf("concurrent claim: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent transfer claims succeeded %d times", successes)
	}
	if session.CharacterID != char.ID {
		t.Fatal("handle was not resolved to authenticated character")
	}
	if _, err = store.ConsumeTransfer(ctx, claim); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatal("transfer replay accepted")
	}
	saved := session.Character
	saved.X += 10
	if err = store.SaveCharacter(ctx, session, saved); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCharacter(ctx, session, saved); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale revision overwrite accepted")
	}
	loaded, err := store.GetCharacter(ctx, char.ID)
	if err != nil || loaded.X != saved.X || loaded.Revision != saved.Revision+1 {
		t.Fatalf("progress persistence: %v", err)
	}
	if _, err = store.AcquireWorldLease(ctx, 126, "test-new-"+suffix); !errors.Is(err, domain.ErrLeaseBusy) {
		t.Fatal("live world ownership stolen")
	}
	if _, err = store.db.ExecContext(ctx, "UPDATE ops_runtime_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE world_id=126 AND instance_id=$1", lease.InstanceID); err != nil {
		t.Fatal(err)
	}
	nextLease, err := store.AcquireWorldLease(ctx, 126, "test-new-"+suffix)
	if err != nil || nextLease.Epoch <= lease.Epoch {
		t.Fatalf("lease takeover: %v", err)
	}
	loaded.Revision++
	if err = store.SaveCharacter(ctx, session, loaded); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale world persisted after takeover")
	}
	req.Name = "Old" + suffix
	req.OperationID = "stale-create-" + suffix
	if _, err = store.CreateCharacter(ctx, req); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale world created a character")
	}
	recovered, err := store.BeginLogin(ctx, acc.ID, "recovered-"+suffix)
	if err != nil {
		t.Fatalf("expired world left account stuck: %v", err)
	}
	if err = store.ReleaseCharacter(ctx, session); err != nil {
		t.Fatal(err)
	} // Must not clear recovered login.
	req.Lease = nextLease
	req.Name = "New" + suffix
	req.OperationID = "new-create-" + suffix
	if _, err = store.CreateCharacter(ctx, req); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale login RPC created character: %v", err)
	}
	if err = store.TouchLogin(ctx, login); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale login renewed current session: %v", err)
	}
	req.LoginSession = recovered
	if _, err = store.CreateCharacter(ctx, req); err != nil {
		t.Fatal(err)
	}
	secondTransfer, err := store.CreateTransfer(ctx, domain.TransferRequest{AccountID: acc.ID, CharacterID: char.ID, WorldID: 126, ChannelID: 1, SessionID: recovered.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatalf("old cleanup revoked recovered login: %v", err)
	}
	secondSession, err := store.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: secondTransfer.ClientHandle, WorldID: 126, ChannelID: 1, ClientIP: "127.0.0.1", Lease: nextLease, AllowLegacyLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	concurrentCtx, concurrentCancel := context.WithTimeout(ctx, 3*time.Second)
	defer concurrentCancel()
	concurrentStart := make(chan struct{})
	saveResult := make(chan error, 1)
	releaseResult := make(chan error, 1)
	go func() {
		<-concurrentStart
		saveResult <- store.SaveCharacter(concurrentCtx, secondSession, secondSession.Character)
	}()
	go func() { <-concurrentStart; releaseResult <- store.ReleaseCharacter(concurrentCtx, secondSession) }()
	close(concurrentStart)
	if err = <-saveResult; err != nil && !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("concurrent save/release: %v", err)
	}
	if err = <-releaseResult; err != nil {
		t.Fatalf("concurrent release/save: %v", err)
	}
	fallbackCfg := cfg
	fallbackCfg.RedisAddr = "127.0.0.1:1"
	fallback, err := Open(ctx, fallbackCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Close()
	roster, err = fallback.ListCharacters(ctx, acc.ID, 126)
	if err != nil || len(roster) != 2 {
		t.Fatalf("Redis outage fallback: count=%d err=%v", len(roster), err)
	}
}
