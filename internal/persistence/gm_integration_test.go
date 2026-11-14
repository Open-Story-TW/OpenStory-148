package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func qaGMFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	useCombatSchema(t) // Generated verified private schema; never public.
	ctx, s, actors := inventoryFixture(t)
	var installed bool
	if err := s.db.QueryRowContext(ctx, `SELECT to_regclass('ops_qa_gm_grants') IS NOT NULL`).Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if installed {
		return ctx, s, actors
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", ".local", "drafts", "0039_qa_gm.sql"))
	if os.IsNotExist(err) {
		raw, err = os.ReadFile(filepath.Join("..", "..", "ent", "migrate", "migrations", "0039_qa_gm.sql"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	return ctx, s, actors
}

func grantQAActor(t *testing.T, ctx context.Context, s *Store, a domain.CharacterSession) domain.CharacterSession {
	t.Helper()
	if _, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline' WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	_, err := s.AuthorizeQAGM(ctx, domain.QAGMGrantRequest{OperationID: fmt.Sprintf("qa-offline-grant-%d", a.CharacterID), Actor: "unit-test-operator", Action: "grant", AccountID: a.AccountID, CharacterID: a.CharacterID, WorldID: a.Lease.WorldID, Role: domain.QAGMRole, ExpiresAt: time.Now().UTC().Add(time.Hour), Reason: "isolated schema automated test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='world' WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestQAGMCommandAtomicReplayAndFreshRevocation(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	normal := actors[1]
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-level-command-%d", a.CharacterID), Name: "level", Args: []int64{10}}
	r, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || !r.Changed || r.Character.Level != 10 || r.Character.EXP != 0 || r.Character.GM != 6 {
		t.Fatalf("level result %+v %v", r, err)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Level != 10 || loaded.Revision != a.Character.Revision+1 {
		t.Fatalf("reload %+v %v", loaded, err)
	}
	replay, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || !replay.Replayed || replay.Changed || replay.MapChanged || replay.Character.Revision != loaded.Revision {
		t.Fatalf("replay %+v %v", replay, err)
	}
	resolved, found, err := s.ResolveGMCommand(ctx, q)
	if err != nil || !found || !resolved.Changed {
		t.Fatalf("outcome resolution %+v %v %v", resolved, found, err)
	}
	altered := q
	altered.Args = []int64{11}
	if _, err = s.ExecuteGMCommand(ctx, altered); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("same operation rebound", err)
	}
	for _, modify := range []func(*domain.GMCommandRequest){func(q *domain.GMCommandRequest) { q.Session = normal; q.Session.Character.GM = 6 }, func(q *domain.GMCommandRequest) { q.Session.Generation++ }, func(q *domain.GMCommandRequest) { q.Session.AccountID++ }} {
		x := q
		x.OperationID += "-deny"
		modify(&x)
		if _, err = s.ExecuteGMCommand(ctx, x); err == nil {
			t.Fatal("forged authority accepted")
		}
	}
	a.Character = loaded
	revoke := domain.QAGMGrantRequest{OperationID: fmt.Sprintf("qa-live-revoke-%d", a.CharacterID), Actor: "unit-test-operator", Action: "revoke", AccountID: a.AccountID, CharacterID: a.CharacterID, WorldID: a.Lease.WorldID, Role: domain.QAGMRole, Reason: "revoke while online"}
	if _, err = s.AuthorizeQAGM(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("revoked old success replay accepted", err)
	}
	q.OperationID += "-revoked"
	q.Session = a
	if _, err = s.ExecuteGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("revoked new command accepted", err)
	}
	var accepted, denied int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE outcome='accepted'),count(*) FILTER(WHERE outcome='denied') FROM ops_qa_gm_commands WHERE character_id=$1`, a.CharacterID).Scan(&accepted, &denied); err != nil || accepted != 1 || denied != 1 {
		t.Fatalf("audit accepted=%d denied=%d %v", accepted, denied, err)
	}
	if n, e := s.GetCharacter(ctx, normal.CharacterID); e != nil || n.GM != 0 || n.Level != 1 {
		t.Fatal("ordinary character changed", e)
	}
}

func TestQAGMExpiredGrantRejectsSuccessfulReplay(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-expired-replay-%d", a.CharacterID), Name: "whereami"}
	if _, err := s.ExecuteGMCommand(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE ops_qa_gm_grants SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("expired replay accepted", err)
	}
}

func TestQAGMGrantOnlineScopeAndAuditRollback(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := actors[0]
	grant := domain.QAGMGrantRequest{OperationID: fmt.Sprintf("qa-grant-denied-%d", a.CharacterID), Actor: "unit-test-operator", Action: "grant", AccountID: a.AccountID, CharacterID: a.CharacterID, WorldID: a.Lease.WorldID, Role: domain.QAGMRole, ExpiresAt: time.Now().Add(time.Hour), Reason: "test"}
	if _, err := s.AuthorizeQAGM(ctx, grant); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatal("online promotion accepted", err)
	}
	grant.CharacterID = actors[1].CharacterID
	if _, err := s.AuthorizeQAGM(ctx, grant); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross account grant accepted", err)
	}
	a = grantQAActor(t, ctx, s, a)
	// Audit is part of the same transaction as growth, never best-effort.
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE ops_qa_gm_commands ADD CONSTRAINT test_reject_audit CHECK (command<>'level')`); err != nil {
		t.Fatal(err)
	}
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-rollback-command-%d", a.CharacterID), Name: "level", Args: []int64{10}}
	if _, err := s.ExecuteGMCommand(ctx, q); err == nil {
		t.Fatal("audit failure accepted")
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Level != a.Character.Level || loaded.Revision != a.Character.Revision || loaded.BaseMaxHP != a.Character.BaseMaxHP {
		t.Fatalf("partial effect %+v %v", loaded, err)
	}
	if _, found, err := s.ResolveGMCommand(ctx, q); err != nil || found {
		t.Fatalf("rollback has receipt found=%v %v", found, err)
	}
}

func TestQAGMConcurrentSameOperationCommitsOnceAndPersistsSkill(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-concurrent-skill-%d", a.CharacterID), Name: "skill", Args: []int64{1002, 3}}
	var wg sync.WaitGroup
	results := make(chan domain.GMCommandResult, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.ExecuteGMCommand(ctx, q)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	fresh, count := 0, 0
	for r := range results {
		count++
		if !r.Replayed {
			fresh++
		}
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || count != 3 || fresh != 1 || learned(loaded, 1002).Level != 3 || loaded.SP != a.Character.SP || loaded.MP != a.Character.MP || len(loaded.SkillBuffs) != 0 || loaded.Revision != a.Character.Revision+1 {
		t.Fatalf("concurrent fresh=%d count=%d %+v %v", fresh, count, loaded, err)
	}
	a.Character = loaded
	q.Session = a
	q.OperationID += "-same-target"
	r, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || r.Changed || r.Character.Revision != loaded.Revision {
		t.Fatal("absolute same skill changed state", err)
	}
}

func TestQAGMAuthorizationReplayCannotReenableRevokedGrant(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := actors[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline' WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	g := domain.QAGMGrantRequest{OperationID: fmt.Sprintf("qa-authorization-replay-%d", a.CharacterID), Actor: "unit-test-operator", Action: "grant", AccountID: a.AccountID, CharacterID: a.CharacterID, WorldID: a.Lease.WorldID, Role: domain.QAGMRole, ExpiresAt: time.Now().Add(time.Hour), Reason: "isolated authorization replay test"}
	tooLong := g
	tooLong.ExpiresAt = time.Now().Add(25 * time.Hour)
	if _, err := s.AuthorizeQAGM(ctx, tooLong); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("overlong grant accepted", err)
	}
	original, err := s.AuthorizeQAGM(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.AuthorizeQAGM(ctx, g)
	if err != nil || !replay.Replayed || replay.Grant.Revision != original.Grant.Revision {
		t.Fatal("grant replay changed authority", err)
	}
	altered := g
	altered.Reason = "changed intent"
	if _, err = s.AuthorizeQAGM(ctx, altered); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("grant operation rebound", err)
	}
	revoke := g
	revoke.OperationID += "-revoke"
	revoke.Action = "revoke"
	if _, err = s.AuthorizeQAGM(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	replay, err = s.AuthorizeQAGM(ctx, g)
	loaded, loadErr := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || !replay.Replayed || replay.Grant.Enabled || loaded.GM != 0 || loadErr != nil {
		t.Fatalf("old grant reinstated authority %+v %+v %v %v", replay, loaded, err, loadErr)
	}
}

func TestQAGMHealCarriesLiveDamageAndSameLevelPersistsNoExtraRevision(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET level=2,exp=5 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-no-reset-exp-%d", a.CharacterID), Name: "level", Args: []int64{2}}
	r, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || r.Changed || r.Character.EXP != 5 || r.Character.Revision != a.Character.Revision {
		t.Fatalf("same level lost natural EXP %+v %v", r, err)
	}
	a.Character.HP = 1
	a.Character.MP = 0
	a.Character.X = 99
	q.Session = a
	q.Name = "heal"
	q.Args = nil
	q.OperationID += "-heal"
	r, err = s.ExecuteGMCommand(ctx, q)
	if err != nil || !r.Changed || r.Character.HP != a.Character.MaxHP || r.Character.MP != a.Character.MaxMP || r.Character.EXP != 5 || r.Character.X != 99 {
		t.Fatalf("heal %+v %v", r, err)
	}
	a.Character = r.Character
	a.Character.HP = 0
	q.Session = a
	q.OperationID += "-dead"
	if _, err = s.ExecuteGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("heal resurrected dead character", err)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.HP != r.Character.HP || loaded.Revision != r.Character.Revision {
		t.Fatal("rejected heal changed durable state", err)
	}
}

func TestQAGMCashExpiryRewritesOnlyOwnedNonPetLockerAndAudits(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	other := actors[1]
	insert := func(account int64, template int, state string) int64 {
		var id int64
		if err := s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) VALUES($1,$2,1,$3,1,$4,0,$5) RETURNING id`, account, a.Lease.WorldID, template, template/1000000, state).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owned := insert(a.AccountID, 5072000, "locker")
	pet := insert(a.AccountID, 5000000, "locker")
	foreign := insert(other.AccountID, 5072000, "locker")
	inventory := insert(a.AccountID, 5072000, "inventory")
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-cash-expiry-%d", a.CharacterID), Name: "cashexpire", Args: []int64{owned, 60}}
	before := time.Now().UnixMilli()
	r, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || r.Changed || r.MapChanged || r.CashExpiry == nil || r.CashExpiry.CashID != owned || r.CashExpiry.TemplateID != 5072000 || r.CashExpiry.PreviousExpiresAt != 0 {
		t.Fatalf("cash expiry %+v %v", r, err)
	}
	var stored int64
	if err = s.db.QueryRowContext(ctx, `SELECT expires_at FROM asset_cash_items WHERE id=$1`, owned).Scan(&stored); err != nil || stored != r.CashExpiry.ExpiresAt || stored < before+59_000 || stored > before+61_000+5_000 {
		t.Fatalf("stored expiry %d result %+v %v", stored, r.CashExpiry, err)
	}
	replay, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || !replay.Replayed || replay.CashExpiry == nil || replay.CashExpiry.ExpiresAt != stored {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if loaded, e := s.GetCharacter(ctx, a.CharacterID); e != nil || loaded.Revision != a.Character.Revision {
		t.Fatal("cash expiry changed character revision", e)
	}
	for i, args := range [][]int64{{pet, 60}, {foreign, 60}, {inventory, 60}, {owned, domain.QACashExpiryMaxSeconds + 1}, {owned, -1}, {0, 60}} {
		x := q
		x.OperationID = fmt.Sprintf("%s-deny-%d", q.OperationID, i)
		x.Args = args
		if _, err = s.ExecuteGMCommand(ctx, x); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("args %v accepted: %v", args, err)
		}
	}
	for _, id := range []int64{pet, foreign, inventory} {
		if err = s.db.QueryRowContext(ctx, `SELECT expires_at FROM asset_cash_items WHERE id=$1`, id).Scan(&stored); err != nil || stored != 0 {
			t.Fatalf("record %d changed to %d %v", id, stored, err)
		}
	}
	normal := q
	normal.Session = other
	normal.OperationID += "-ungranted"
	normal.Args = []int64{foreign, 60}
	if _, err = s.ExecuteGMCommand(ctx, normal); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("ungranted character rewrote expiry", err)
	}
	var accepted, denied int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE outcome='accepted' AND command='cashexpire' AND result->'cash_expiry'->>'cash_id'=$2::text),count(*) FILTER(WHERE outcome='denied') FROM ops_qa_gm_commands WHERE character_id=$1`, a.CharacterID, fmt.Sprint(owned)).Scan(&accepted, &denied); err != nil || accepted != 1 || denied != 6 {
		t.Fatalf("audit accepted=%d denied=%d %v", accepted, denied, err)
	}
}

func TestQAGMPermanentGrantHasNoTimeLimitAndCanBeRevoked(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := actors[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline' WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	g := domain.QAGMGrantRequest{OperationID: fmt.Sprintf("qa-permanent-grant-%d", a.CharacterID), Actor: "unit-test-operator", Action: "grant", AccountID: a.AccountID, CharacterID: a.CharacterID, WorldID: a.Lease.WorldID, Role: domain.QAGMRole, Permanent: true, Reason: "permanent grant test"}
	granted, err := s.AuthorizeQAGM(ctx, g)
	if err != nil || !granted.Grant.Enabled || !granted.Grant.ExpiresAt.Equal(domain.QAGMPermanentExpiry) {
		t.Fatalf("permanent grant=%+v err=%v", granted, err)
	}
	var stored time.Time
	var rank int
	if err = s.db.QueryRowContext(ctx, `SELECT g.expires_at,c.gm FROM ops_qa_gm_grants g JOIN game_characters c ON c.id=g.character_id WHERE g.character_id=$1`, a.CharacterID).Scan(&stored, &rank); err != nil || !stored.Equal(domain.QAGMPermanentExpiry) || rank != domain.QAGMRank {
		t.Fatalf("stored expiry=%v rank=%d err=%v", stored, rank, err)
	}
	c := domain.Character{ID: a.CharacterID, AccountID: a.AccountID, WorldID: a.Lease.WorldID, GM: domain.QAGMRank}
	if !validQAGMGrant(granted.Grant, c, time.Now().Add(10*365*24*time.Hour)) {
		t.Fatal("permanent grant is not valid ten years on")
	}
	g.OperationID, g.Action, g.Permanent = fmt.Sprintf("qa-permanent-revoke-%d", a.CharacterID), "revoke", false
	revoked, err := s.AuthorizeQAGM(ctx, g)
	if err != nil || revoked.Grant.Enabled {
		t.Fatalf("revoke=%+v err=%v", revoked, err)
	}
	if err = s.db.QueryRowContext(ctx, `SELECT gm FROM game_characters WHERE id=$1`, a.CharacterID).Scan(&rank); err != nil || rank != 0 {
		t.Fatalf("rank after revoke=%d err=%v", rank, err)
	}
}
