package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func int64ptr(n int64) *int64 { return &n }

func TestScriptLogsAtomicRewardsAndScope(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	name := fmt.Sprintf("script-test-%d", sessions[0].CharacterID)
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), "DELETE FROM game_script_logs WHERE name LIKE $1", name+"%")
	})
	key := domain.ScriptLogKey{Scope: domain.LogWorld, Period: domain.LogDaily, Name: name}
	requests := []domain.ScriptExchange{
		{Session: sessions[0], OperationID: name + "-a", Logs: []domain.ScriptLogChange{{Key: key, Delta: 1, Expected: int64ptr(0), Maximum: int64ptr(1)}}, MesosDelta: 100, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 2}}},
		{Session: sessions[1], OperationID: name + "-b", Logs: []domain.ScriptLogChange{{Key: key, Delta: 1, Expected: int64ptr(0), Maximum: int64ptr(1)}}, MesosDelta: 100, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 2}}},
	}
	var wg sync.WaitGroup
	type outcome struct {
		index  int
		result domain.ScriptExchangeResult
		err    error
	}
	out := make(chan outcome, 2)
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := s.ExecuteScriptExchange(ctx, requests[i])
			out <- outcome{i, r, e}
		}(i)
	}
	wg.Wait()
	close(out)
	winner := -1
	for o := range out {
		if o.err == nil {
			if winner != -1 {
				t.Fatal("world reward committed twice")
			}
			winner = o.index
			sessions[o.index].Character = o.result.Character
		} else if !errors.Is(o.err, domain.ErrAlreadyExists) {
			t.Fatalf("loser error=%v", o.err)
		}
	}
	if winner < 0 {
		t.Fatal("neither world reward succeeded")
	}
	for i := range sessions {
		v, e := s.GetCharacter(ctx, sessions[i].CharacterID)
		if e != nil {
			t.Fatal(e)
		}
		mesos := int64(1000)
		qty := 5
		if i == winner {
			mesos = 1100
			qty = 7
		}
		if v.Mesos != mesos || len(v.Items) != 1 || v.Items[0].Quantity != qty {
			t.Fatalf("actor%d assets %+v", i, v)
		}
		logs, e := s.ReadScriptLogs(ctx, sessions[i], []domain.ScriptLogKey{key})
		if e != nil || len(logs) != 1 || logs[0].Value != 1 {
			t.Fatalf("shared log=%+v error=%v", logs, e)
		}
	}
	// A lost response can be retried concurrently even with the original revision.
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.ExecuteScriptExchange(ctx, requests[winner])
			if e != nil || !r.Replayed || r.Character.Mesos != 1100 {
				t.Errorf("idempotent replay=%+v error=%v", r, e)
			}
		}()
	}
	wg.Wait()
	altered := requests[winner]
	altered.MesosDelta = 200
	if _, e := s.ExecuteScriptExchange(ctx, altered); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("altered command replay=%v", e)
	}
	// Failed inventory capacity/quantity must roll back a previously updated log.
	rollbackKey := key
	rollbackKey.Name = name + "-rollback"
	failed := domain.ScriptExchange{Session: sessions[winner], OperationID: name + "-insufficient", Logs: []domain.ScriptLogChange{{Key: rollbackKey, Delta: 1}}, MesosDelta: 10, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -100}}}
	if _, e := s.ExecuteScriptExchange(ctx, failed); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("insufficient items=%v", e)
	}
	logs, e := s.ReadScriptLogs(ctx, sessions[winner], []domain.ScriptLogKey{rollbackKey})
	if e != nil || logs[0].Value != 0 || logs[0].Revision != 0 {
		t.Fatalf("rollback log leaked: %+v error=%v", logs, e)
	}
	// All three scopes accept independent calendar periods and persist value.
	for i, scope := range []domain.LogScope{domain.LogCharacter, domain.LogAccount, domain.LogWorld} {
		for j, period := range []domain.LogPeriod{domain.LogPermanent, domain.LogHourly, domain.LogDaily, domain.LogWeekly, domain.LogMonthly, domain.LogYearly} {
			k := domain.ScriptLogKey{Scope: scope, Period: period, Name: fmt.Sprintf("%s-%d-%d", name, i, j)}
			r, e := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: sessions[winner], OperationID: fmt.Sprintf("%s-scope-%d-%d", name, i, j), Logs: []domain.ScriptLogChange{{Key: k, Set: int64ptr(42)}}})
			if e != nil {
				t.Fatal(e)
			}
			sessions[winner].Character = r.Character
			read, e := s.ReadScriptLogs(ctx, sessions[winner], []domain.ScriptLogKey{k})
			if e != nil || read[0].Value != 42 {
				t.Fatalf("scope/period=%+v error=%v", read, e)
			}
			other, e := s.ReadScriptLogs(ctx, sessions[1-winner], []domain.ScriptLogKey{k})
			if e != nil {
				t.Fatal(e)
			}
			want := int64(0)
			if scope == domain.LogWorld {
				want = 42
			}
			if other[0].Value != want {
				t.Fatalf("scope isolation: %s got %d want %d", scope, other[0].Value, want)
			}
		}
	}
}

func TestScriptReceiptFailureRollsBackEveryEffect(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	name := fmt.Sprintf("script-fail-%d", sessions[0].CharacterID)
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), "DELETE FROM game_script_logs WHERE name=$1", name)
	})
	fn := fmt.Sprintf("script_fail_%d", sessions[0].CharacterID)
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected script receipt error'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_script_commands FOR EACH ROW WHEN(NEW.character_id=%d) EXECUTE FUNCTION %s()`, fn, fn, sessions[0].CharacterID, fn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, e := s.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_script_commands; DROP FUNCTION IF EXISTS %s()", fn, fn))
		if e != nil {
			t.Error(e)
		}
	})
	key := domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: name}
	_, err = s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: sessions[0], OperationID: name, Logs: []domain.ScriptLogChange{{Key: key, Delta: 1}}, MesosDelta: 50, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 2}}})
	if err == nil {
		t.Fatal("receipt failure ignored")
	}
	value, err := s.GetCharacter(ctx, sessions[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if value.Mesos != 1000 || value.Items[0].Quantity != 5 || value.Revision != sessions[0].Character.Revision {
		t.Fatalf("failed transaction changed assets: %+v", value)
	}
	logs, err := s.ReadScriptLogs(ctx, sessions[0], []domain.ScriptLogKey{key})
	if err != nil || logs[0].Value != 0 || logs[0].Revision != 0 {
		t.Fatalf("failed transaction changed log: %+v error=%v", logs, err)
	}
}
