package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func potionTradeItem(c domain.Character, quantity int) domain.TradeItem {
	return domain.TradeItem{InvType: 2, Slot: 1, Quantity: quantity, InstanceID: c.Items[0].ID, TradeSlot: 1}
}

func TestTradeReceiptResolutionCommittedAbsentAndObsolete(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	r := domain.TradeSettlement{ID: fmt.Sprintf("trade-resolve-%d-%d", a.CharacterID, b.CharacterID), First: a, Second: b, FirstOffer: domain.TradeOffer{Mesos: 100, Items: []domain.TradeItem{potionTradeItem(a.Character, 2)}}}
	if _, committed, err := s.ResolveTradeReceipt(ctx, r); err != nil || committed {
		t.Fatalf("absent=%v %v", committed, err)
	}
	actual, err := s.SettleTrade(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	resolved, committed, err := s.ResolveTradeReceipt(ctx, r)
	if err != nil || !committed || !resolved.Replayed || resolved.First.Character.Mesos != actual.First.Character.Mesos || len(resolved.First.Changes) == 0 || len(resolved.Second.Changes) == 0 {
		t.Fatalf("committed resolution=%+v %v %v", resolved, committed, err)
	}
	wrong := r
	wrong.FirstOffer.Mesos++
	if _, _, err = s.ResolveTradeReceipt(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed receipt=%v", err)
	}
	stale := r
	stale.First.Generation--
	if _, _, err = s.ResolveTradeReceipt(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale receipt access=%v", err)
	}
	a.Character = actual.First.Character
	if _, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 9031, InvType: 2, Source: 1, Destination: 2, Quantity: -1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ResolveTradeReceipt(ctx, r); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("obsolete receipt delta=%v", err)
	}
	loaded, err := s.GetCharacter(ctx, b.CharacterID)
	if err != nil || loaded.Mesos != 1100 {
		t.Fatalf("resolution mutated/repeated trade %+v %v", loaded, err)
	}
}

func TestTradeReceiptResolutionWaitsForOriginalCommitOrRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback_%v", rollback), func(t *testing.T) {
			ctx, s, sessions := inventoryFixture(t)
			a, b := sessions[0], sessions[1]
			r := domain.TradeSettlement{ID: fmt.Sprintf("trade-resolve-wait-%d", a.CharacterID), First: a, Second: b, FirstOffer: domain.TradeOffer{Mesos: 100}}
			gate, err := s.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			key := int64(1000000 + a.CharacterID%1000000000)
			if _, err = gate.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
				t.Fatal(err)
			}
			defer gate.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
			fn := fmt.Sprintf("trade_resolve_gate_%d", a.CharacterID)
			tail := "RETURN NEW;"
			if rollback {
				tail = "RAISE EXCEPTION 'test original trade rolls back';"
			}
			if _, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); %s END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_trade_settlements FOR EACH ROW WHEN(NEW.first_character_id=%d) EXECUTE FUNCTION %s()`, fn, key, tail, fn, a.CharacterID, fn)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = s.db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON asset_trade_settlements; DROP FUNCTION IF EXISTS %s()`, fn, fn))
			})
			original := make(chan error, 1)
			go func() { _, err := s.SettleTrade(ctx, r); original <- err }()
			waiting := false
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid=0 AND objid=$1 AND NOT granted)`, key).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !waiting {
				t.Fatal("original transaction did not reach its receipt gate")
			}
			type resolution struct {
				committed bool
				err       error
			}
			done := make(chan resolution, 1)
			go func() { _, committed, err := s.ResolveTradeReceipt(ctx, r); done <- resolution{committed, err} }()
			select {
			case v := <-done:
				t.Fatalf("receipt bypassed original transaction locks:%+v", v)
			case <-time.After(20 * time.Millisecond):
			}
			if _, err = gate.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
				t.Fatal(err)
			}
			if err = <-original; (err != nil) != rollback {
				t.Fatalf("original rollback%v:%v", rollback, err)
			}
			resolved := <-done
			if resolved.err != nil || resolved.committed == rollback {
				t.Fatalf("after original transaction:%+v", resolved)
			}
			loaded, err := s.GetCharacter(ctx, a.CharacterID)
			want := int64(900)
			if rollback {
				want = 1000
			}
			if err != nil || loaded.Mesos != want {
				t.Fatalf("assets after resolve:%+v %v", loaded, err)
			}
		})
	}
}

func TestTradeAtomicExchangeAndReplay(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	id := fmt.Sprintf("trade-test-%d-%d", a.CharacterID, b.CharacterID)
	r := domain.TradeSettlement{ID: id, First: a, Second: b, FirstOffer: domain.TradeOffer{Mesos: 100, Items: []domain.TradeItem{potionTradeItem(a.Character, 2)}}, SecondOffer: domain.TradeOffer{Mesos: 200, Items: []domain.TradeItem{potionTradeItem(b.Character, 1)}}}
	first, err := s.SettleTrade(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.First.Character.Mesos != 1100 || first.Second.Character.Mesos != 900 || first.First.Character.Items[0].Quantity != 4 || first.Second.Character.Items[0].Quantity != 6 {
		t.Fatalf("exchange=%+v", first)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, e := s.SettleTrade(ctx, r)
			if e != nil || !result.Replayed || result.First.Character.Revision != first.First.Character.Revision {
				t.Errorf("replay=%+v %v", result, e)
			}
		}()
	}
	wg.Wait()
	wrong := r
	wrong.FirstOffer.Mesos++
	if _, err = s.SettleTrade(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay=%v", err)
	}
	for _, character := range []domain.Character{first.First.Character, first.Second.Character} {
		loaded, e := s.GetCharacter(ctx, character.ID)
		if e != nil || loaded.Mesos != character.Mesos || loaded.Items[0].Quantity != character.Items[0].Quantity {
			t.Fatalf("reload=%+v %v", loaded, e)
		}
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_trade_settlements WHERE trade_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipts=%d %v", count, err)
	}
}

func TestTradeCapacityAndReceiptFailureRollback(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	id := fmt.Sprintf("trade-rollback-%d-%d", a.CharacterID, b.CharacterID)
	r := domain.TradeSettlement{ID: id, First: a, Second: b, FirstOffer: domain.TradeOffer{Mesos: 100, Items: []domain.TradeItem{potionTradeItem(a.Character, 2)}}}
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_items SET quantity=300 WHERE character_id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	for slot := 2; slot <= 24; slot++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,2000000,300)`, b.CharacterID, slot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SettleTrade(ctx, r); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("capacity failure=%v", err)
	}
	unchanged, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || unchanged.Mesos != 1000 || unchanged.Items[0].Quantity != 5 || unchanged.Revision != a.Character.Revision {
		t.Fatalf("capacity rollback=%+v %v", unchanged, err)
	}
	// Remove only this fixture's padding; fail after transfers at receipt insert.
	if _, err = s.db.ExecContext(ctx, `DELETE FROM asset_items WHERE character_id=$1 AND slot>1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_items SET quantity=5 WHERE character_id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	fn := fmt.Sprintf("trade_failure_%d", a.CharacterID)
	if _, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test trade receipt failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_trade_settlements FOR EACH ROW WHEN (NEW.first_character_id=%d) EXECUTE FUNCTION %s()`, fn, fn, a.CharacterID, fn)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_trade_settlements; DROP FUNCTION IF EXISTS %s()", fn, fn))
	})
	if _, err = s.SettleTrade(ctx, r); err == nil {
		t.Fatal("receipt failure ignored")
	}
	for _, session := range sessions {
		loaded, e := s.GetCharacter(ctx, session.CharacterID)
		if e != nil || loaded.Mesos != 1000 || loaded.Items[0].Quantity != 5 || loaded.Revision != session.Character.Revision {
			t.Fatalf("receipt rollback=%+v %v", loaded, e)
		}
	}
}
