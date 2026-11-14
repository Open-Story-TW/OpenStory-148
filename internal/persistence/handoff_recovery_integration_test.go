package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
)

const handoffWorld = 126

// useHandoffSchema runs against a generated private schema so lease and
// session fault injection never touches the live world/cashshop rows.
func useHandoffSchema(t *testing.T) {
	t.Helper()
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("isolated PostgreSQL required")
	}
	original := os.Getenv("MAPLEGO_DATABASE_URL")
	admin, err := sql.Open("pgx", original)
	if err != nil {
		t.Fatal("open test database")
	}
	suffix, err := randomToken(6)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^test_handoff_[0-9a-f]{12}$`)
	schema := "test_handoff_" + suffix
	if !pattern.MatchString(schema) {
		t.Fatal("invalid generated test schema")
	}
	if _, err = admin.ExecContext(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if !pattern.MatchString(schema) {
			t.Error("refusing unexpected schema cleanup")
			return
		}
		if _, e := admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); e != nil {
			t.Error("private schema cleanup", e)
		}
	})
	uri, err := url.Parse(original)
	if err != nil || uri.Scheme != "postgres" && uri.Scheme != "postgresql" {
		t.Fatal("private schema requires a PostgreSQL URI")
	}
	params := uri.Query()
	params.Set("search_path", schema)
	uri.RawQuery = params.Encode()
	probe, err := sql.Open("pgx", uri.String())
	if err != nil {
		t.Fatal("open private schema connection")
	}
	defer probe.Close()
	var current string
	if err = probe.QueryRow(`SELECT current_schema()`).Scan(&current); err != nil || current != schema {
		t.Fatal("search_path did not select the generated test schema")
	}
	if err = migrate.Apply(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_DATABASE_URL", uri.String())
	t.Setenv("MAPLEGO_WZ_ROOT", "")
}

type handoffFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *Store
	account int64
	char    int64
	suffix  string
	logins  int
}

func newHandoffFixture(t *testing.T) *handoffFixture {
	useHandoffSchema(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	store, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 6})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := store.Register(ctx, "ho"+suffix, "Handoff145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		store.roster.Invalidate(cleanup, acc.ID, handoffWorld)
	})
	if err = store.ConfigureAccount(ctx, acc.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	f := &handoffFixture{t: t, ctx: ctx, store: store, account: acc.ID, suffix: suffix}
	login := f.login()
	lease := f.worldLease("world-a")
	chr, err := store.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: acc.ID, WorldID: handoffWorld, Name: "Ho" + suffix, Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "handoff-create-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	f.char = chr.ID
	if err = store.EndLogin(ctx, login); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *handoffFixture) login() domain.LoginSession {
	f.t.Helper()
	f.logins++
	login, err := f.store.BeginLogin(f.ctx, f.account, "handoff-login-"+f.suffix+"-"+string(rune('a'+f.logins)))
	if err != nil {
		f.t.Fatalf("login %d: %v", f.logins, err)
	}
	return login
}

func (f *handoffFixture) worldLease(instance string) domain.WorldLease {
	f.t.Helper()
	lease, err := f.store.AcquireWorldLease(f.ctx, handoffWorld, instance+"-"+f.suffix)
	if err != nil {
		f.t.Fatal(err)
	}
	return lease
}

// enter performs a full login -> world claim on channel 1.
func (f *handoffFixture) enter(lease domain.WorldLease) domain.CharacterSession {
	f.t.Helper()
	login := f.login()
	tr, err := f.store.CreateTransfer(f.ctx, domain.TransferRequest{AccountID: f.account, CharacterID: f.char, WorldID: handoffWorld, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		f.t.Fatal(err)
	}
	session, err := f.store.ConsumeTransfer(f.ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: handoffWorld, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return session
}

func (f *handoffFixture) expectOnline(step string) {
	f.t.Helper()
	if _, err := f.store.BeginLogin(f.ctx, f.account, "handoff-dup-"+f.suffix); !errors.Is(err, domain.ErrAlreadyOnline) {
		f.t.Fatalf("%s: second login=%v", step, err)
	}
}

func (f *handoffFixture) stored() domain.Character {
	f.t.Helper()
	value, err := readCharacterSQL(f.ctx, f.store.db, f.char)
	if err != nil {
		f.t.Fatal(err)
	}
	return value
}

func (f *handoffFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.store.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func TestChannelHandoffResendAfterLostCommit(t *testing.T) {
	f := newHandoffFixture(t)
	ctx, store := f.ctx, f.store
	lease := f.worldLease("world-a")
	session := f.enter(lease)
	f.expectOnline("in world")
	if _, err := store.ResumeChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("resume before any transfer=%v", err)
	}
	if err := store.BeginChannelTransfer(ctx, session, 2); err != nil {
		t.Fatal(err)
	}
	f.expectOnline("during channel transfer")
	if _, err := store.ResumeChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("resume of uncommitted transfer=%v", err)
	}
	next, err := store.FinishChannelTransfer(ctx, session, 2)
	if err != nil {
		t.Fatal(err)
	}
	// The reply is lost: a resend of the same Finish must not advance again.
	if _, err = store.FinishChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("second finish=%v", err)
	}
	for range 2 {
		resumed, e := store.ResumeChannelTransfer(ctx, session, 2)
		if e != nil {
			t.Fatal(e)
		}
		if resumed.Generation != next.Generation || resumed.ChannelID != 2 || resumed.SessionID != next.SessionID {
			t.Fatalf("resumed %+v want %+v", resumed, next)
		}
	}
	if _, err = store.ResumeChannelTransfer(ctx, session, 3); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("resume to wrong channel=%v", err)
	}
	stale := lease
	stale.Epoch++
	wrongLease := session
	wrongLease.Lease = stale
	if _, err = store.ResumeChannelTransfer(ctx, wrongLease, 2); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("resume under foreign epoch=%v", err)
	}
	// Source cleanup with the frozen generation cannot free the destination.
	if err = store.ReleaseCharacter(ctx, session); err != nil {
		t.Fatal(err)
	}
	f.expectOnline("after stale source release")
	if err = store.SaveCharacter(ctx, next, next.Character); err != nil {
		t.Fatalf("resumed destination cannot save: %v", err)
	}
	if err = store.ReleaseCharacter(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ResumeChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("resume after release=%v", err)
	}
	f.login()
}

func TestOrphanWorldSessionIsListedAndReleased(t *testing.T) {
	f := newHandoffFixture(t)
	ctx, store := f.ctx, f.store
	lease := f.worldLease("world-a")
	// A claim committed but the socket that would own it is gone.
	session := f.enter(lease)
	owned, err := store.ListWorldSessions(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].AccountID != f.account || owned[0].CharacterID != f.char || owned[0].Generation != session.Generation || owned[0].ChannelID != 1 || owned[0].SessionID != session.SessionID {
		t.Fatalf("owned=%+v session=%+v", owned, session)
	}
	other := lease
	other.Epoch++
	if owned, err = store.ListWorldSessions(ctx, other); err != nil || len(owned) != 0 {
		t.Fatalf("foreign epoch owns %+v err=%v", owned, err)
	}
	f.expectOnline("orphan blocks login")
	orphan := domain.CharacterSession{AccountID: f.account, CharacterID: f.char, SessionID: session.SessionID, Generation: session.Generation, Lease: lease}
	if err = store.ReleaseCharacter(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if owned, err = store.ListWorldSessions(ctx, lease); err != nil || len(owned) != 0 {
		t.Fatalf("released session still owned %+v err=%v", owned, err)
	}
	// A late release of an older generation cannot evict a newer claim.
	again := f.enter(lease)
	if err = store.ReleaseCharacter(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if owned, err = store.ListWorldSessions(ctx, lease); err != nil || len(owned) != 1 || owned[0].Generation != again.Generation {
		t.Fatalf("newer claim evicted: %+v err=%v", owned, err)
	}
}

func TestWorldCrashMidChannelHandoffConservesAssets(t *testing.T) {
	f := newHandoffFixture(t)
	ctx, store := f.ctx, f.store
	lease := f.worldLease("world-a")
	session := f.enter(lease)
	value, err := store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: 777, OperationID: "handoff-crash-meso-" + f.suffix, Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	session.Character = value
	// prepareChannel checkpoints dirty progress before freezing the writer.
	progress := session.Character
	progress.X, progress.Y = 512, -64
	if err = store.SaveCharacter(ctx, session, progress); err != nil {
		t.Fatal(err)
	}
	progress.Revision++
	session.Character = progress
	if err = store.BeginChannelTransfer(ctx, session, 2); err != nil {
		t.Fatal(err)
	}
	f.expectOnline("handoff in flight")
	// The world process dies: its lease lapses and the frozen transfer row
	// reaches its 30 second deadline without a destination claim.
	f.exec(`UPDATE ops_runtime_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE world_id=$1`, handoffWorld)
	f.exec(`UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account)
	if _, err = store.FinishChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("dead world finished transfer: %v", err)
	}
	if _, err = store.ResumeChannelTransfer(ctx, session, 2); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("dead world resumed transfer: %v", err)
	}
	restarted := f.worldLease("world-b")
	if restarted.Epoch <= lease.Epoch {
		t.Fatalf("restart epoch %d <= %d", restarted.Epoch, lease.Epoch)
	}
	if err = store.SaveCharacter(ctx, session, session.Character); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("dead world saved after restart: %v", err)
	}
	back := f.enter(restarted)
	if back.Character.Mesos != value.Mesos || back.Character.X != 512 || back.Character.Y != -64 {
		t.Fatalf("crash lost state: mesos=%d/%d pos=%d,%d", back.Character.Mesos, value.Mesos, back.Character.X, back.Character.Y)
	}
	if got := f.stored(); got.Mesos != value.Mesos {
		t.Fatalf("stored mesos=%d want %d", got.Mesos, value.Mesos)
	}
}

func TestCashshopHandoffResendTimeoutAndOrphan(t *testing.T) {
	f := newHandoffFixture(t)
	ctx, store := f.ctx, f.store
	lease := f.worldLease("world-a")
	cash, err := store.AcquireCashLease(ctx, "cash-a-"+f.suffix)
	if err != nil {
		t.Fatal(err)
	}
	session := f.enter(lease)
	value, err := store.AdjustMesos(ctx, domain.MesoAdjustment{Session: session, Delta: 333, OperationID: "handoff-cash-meso-" + f.suffix, Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	session.Character = value
	const handle = 1450021
	// Timeout: the client never reaches the cashshop.
	if _, err = store.BeginCashTransfer(ctx, session, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	f.expectOnline("cash ticket pending")
	if _, err = store.BeginCashTransfer(ctx, session, handle, "127.0.0.1"); err == nil {
		t.Fatal("frozen world session issued a second cash ticket")
	}
	f.exec(`UPDATE ops_service_transfers SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account)
	f.exec(`UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account)
	if _, err = store.ConsumeCashTransfer(ctx, handle, "127.0.0.1", cash); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("expired cash ticket consumed: %v", err)
	}
	if got := f.stored(); got.Mesos != value.Mesos {
		t.Fatalf("timeout changed mesos %d want %d", got.Mesos, value.Mesos)
	}
	// Resend: two arrivals with the same ticket, only one owns the cashshop.
	session = f.enter(lease)
	if _, err = store.BeginCashTransfer(ctx, session, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ConsumeCashTransfer(ctx, handle, "127.0.0.1", cash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ConsumeCashTransfer(ctx, handle, "127.0.0.1", cash); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("resent cash ticket=%v", err)
	}
	f.expectOnline("in cashshop")
	// The claim's reply was lost, so no socket owns it: recovery lists and
	// releases it under the same cashshop lease.
	owned, err := store.ListCashSessions(ctx, cash)
	if err != nil || len(owned) != 1 || owned[0].Generation != claimed.Session.Generation || owned[0].SessionID != claimed.Session.SessionID || owned[0].CharacterID != f.char {
		t.Fatalf("cash owned=%+v err=%v claimed=%+v", owned, err, claimed.Session)
	}
	if world, e := store.ListWorldSessions(ctx, lease); e != nil || len(world) != 0 {
		t.Fatalf("cash session listed as world-owned: %+v err=%v", world, e)
	}
	orphan := domain.CashSession{Session: domain.CharacterSession{AccountID: owned[0].AccountID, CharacterID: owned[0].CharacterID, SessionID: owned[0].SessionID, Generation: owned[0].Generation}, Lease: cash}
	if err = store.ReleaseCashCharacter(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if owned, err = store.ListCashSessions(ctx, cash); err != nil || len(owned) != 0 {
		t.Fatalf("released cash session still owned %+v err=%v", owned, err)
	}
	if _, err = store.BeginCashReturn(ctx, claimed, "127.0.0.1"); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("released cash session can still return: %v", err)
	}
	// Cashshop crash mid-return: the return ticket expires with its epoch and
	// the character is still fully durable for the next login.
	session = f.enter(lease)
	if _, err = store.BeginCashTransfer(ctx, session, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ConsumeCashTransfer(ctx, handle, "127.0.0.1", cash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.BeginCashReturn(ctx, claimed, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	f.expectOnline("cash return pending")
	f.exec(`UPDATE ops_runtime_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE world_id=$1`, handoffWorld)
	f.exec(`UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account)
	restarted := f.worldLease("world-b")
	if _, matched, e := store.ConsumeCashReturn(ctx, handle, handoffWorld, claimed.ReturnChannel, "127.0.0.1", restarted); !matched || !errors.Is(e, domain.ErrTransferInvalid) {
		t.Fatalf("old-epoch return ticket matched=%v err=%v", matched, e)
	}
	back := f.enter(restarted)
	if back.Character.Mesos != value.Mesos {
		t.Fatalf("cash round trip mesos=%d want %d", back.Character.Mesos, value.Mesos)
	}
}
