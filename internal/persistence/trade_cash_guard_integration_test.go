package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"maple-go-server/internal/domain"
)

// Reproduce the real native policy counterexample through the durable cash
// locker -> physical inventory -> world return APIs, never by editing a
// Character.CashID mock. All records/migrations live in a verified private
// schema and Redis is disabled; this is a repository test, not MCP acceptance.
func TestNativeCashSerialTradeGuard(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		template, inv, quantity int
	}{
		{"bundle", 2000000, 2, 2},
		{"equipment", 1302000, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s, a, b, cashID, source := cashSerialFixture(t, tc.name, tc.template, tc.inv, tc.quantity)
			source.Quantity = 1
			beforeA, err := s.GetCharacter(ctx, a.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			beforeB, err := s.GetCharacter(ctx, b.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			a.Character, b.Character = beforeA, beforeB
			request := domain.TradeSettlement{ID: fmt.Sprintf("trade-cash-guard-%s-%d-%d", tc.name, a.CharacterID, b.CharacterID), First: a, Second: b,
				FirstOffer: domain.TradeOffer{Mesos: 100, Items: []domain.TradeItem{potionTradeItem(a.Character, 1), source}}, SecondOffer: domain.TradeOffer{Mesos: 200}}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := s.SettleTrade(ctx, request)
				if !errors.Is(err, domain.ErrForbidden) {
					var receipts int
					_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_trade_settlements WHERE trade_id=$1`, request.ID).Scan(&receipts)
					t.Fatalf("UNSAFE cash-serial trade accepted or wrong refusal: err=%v committed_receipts=%d source_mesos=%d recipient_mesos=%d source_revision=%d recipient_revision=%d", err, receipts, result.First.Character.Mesos, result.Second.Character.Mesos, result.First.Character.Revision, result.Second.Character.Revision)
				}
			}
			for _, want := range []domain.Character{beforeA, beforeB} {
				got, e := s.GetCharacter(ctx, want.ID)
				if e != nil || got.Revision != want.Revision || got.Mesos != want.Mesos || !reflect.DeepEqual(got.Items, want.Items) || !reflect.DeepEqual(got.Equipment, want.Equipment) {
					t.Fatalf("forbidden trade mutated assets/revisions: character=%d err=%v", want.ID, e)
				}
			}
			var receipts, ledger int
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_trade_settlements WHERE trade_id=$1`, request.ID).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("forbidden trade receipt=%d err=%v", receipts, err)
			}
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_meso_ledger WHERE account_id IN($1,$2) AND operation_id LIKE $3`, a.AccountID, b.AccountID, "%"+request.ID+"%").Scan(&ledger); err != nil || ledger != 0 {
				t.Fatalf("forbidden trade money ledger=%d err=%v", ledger, err)
			}
			var cashOwner int64
			var cashQuantity int
			var state string
			if err = s.db.QueryRowContext(ctx, `SELECT account_id,quantity,state FROM asset_cash_items WHERE id=$1`, cashID).Scan(&cashOwner, &cashQuantity, &state); err != nil || cashOwner != a.AccountID || cashQuantity != tc.quantity || state != "inventory" {
				t.Fatalf("cash serial changed: owner=%d quantity=%d state=%s err=%v", cashOwner, cashQuantity, state, err)
			}
			resolved, committed, e := s.ResolveTradeReceipt(ctx, request)
			if e != nil || committed || resolved.Replayed {
				t.Fatalf("forbidden trade resolved as committed=%v err=%v", committed, e)
			}
			t.Log("ErrForbidden twice; preceding ordinary-item removal, mesos, revisions, cash ownership/quantity and settlement/meso ledgers all unchanged; no committed receipt")
		})
	}
}

// Run the existing positive trade and cash-move contracts in isolated schemas
// to ensure the trade-only guard preserves ordinary assets and locker moves.
func TestNativeCashSerialTradeGuardPreservesSupportedFlows(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"ordinary_trade", testOrdinaryTradeAfterCashGuard},
		{"cash_locker_round_trip", TestNativeCashMoveBindsInventoryTypeAndTargetSlot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wzRoot := os.Getenv("MAPLEGO_WZ_ROOT")
			useCombatSchema(t)
			t.Setenv("MAPLEGO_WZ_ROOT", wzRoot)
			t.Setenv("MAPLEGO_REDIS_ADDR", "")
			t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
			tc.run(t)
		})
	}
}

func testOrdinaryTradeAfterCashGuard(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a, b := actors[0], actors[1]
	r := domain.TradeSettlement{ID: fmt.Sprintf("cash-guard-normal-trade-%d-%d", a.CharacterID, b.CharacterID), First: a, Second: b,
		FirstOffer:  domain.TradeOffer{Mesos: 100, Items: []domain.TradeItem{potionTradeItem(a.Character, 2)}},
		SecondOffer: domain.TradeOffer{Mesos: 200, Items: []domain.TradeItem{potionTradeItem(b.Character, 1)}}}
	first, err := s.SettleTrade(ctx, r)
	if err != nil || first.First.Character.Mesos != 1100 || first.Second.Character.Mesos != 900 || first.First.Character.Items[0].Quantity != 4 || first.Second.Character.Items[0].Quantity != 6 {
		t.Fatalf("ordinary exchange: %+v err=%v", first, err)
	}
	replay, err := s.SettleTrade(ctx, r)
	if err != nil || !replay.Replayed || replay.First.Character.Revision != first.First.Character.Revision || replay.Second.Character.Revision != first.Second.Character.Revision {
		t.Fatalf("ordinary replay: %+v err=%v", replay, err)
	}
}

// cashSerialFixture puts one real cash serial into the first character's
// inventory, slot 3, through the locker and the cash shop return, and offers
// it whole in trade slot 2.
func cashSerialFixture(t *testing.T, name string, template, inv, quantity int) (context.Context, *Store, domain.CharacterSession, domain.CharacterSession, int64, domain.TradeItem) {
	t.Helper()
	wzRoot := os.Getenv("MAPLEGO_WZ_ROOT")
	useCombatSchema(t)
	t.Setenv("MAPLEGO_WZ_ROOT", wzRoot)
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	cashLease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	if _, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", cashLease)
	if err != nil {
		t.Fatal(err)
	}
	var cashID int64
	var equipment []byte
	if inv == 1 {
		definition, e := inventoryDefinition(template)
		if e != nil {
			t.Fatal(e)
		}
		equipment, e = json.Marshal(definition.Equipment)
		if e != nil {
			t.Fatal(e)
		}
	}
	// The same explicit test-only locker fixture as the pre-existing native
	// target/type test. Production cash delivery still requires paid orders.
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,equipment,state) VALUES($1,$2,1,$3,$4,$5,$6,'locker') RETURNING id`, a.AccountID, a.Lease.WorldID, template, quantity, inv, nullableJSON(equipment)).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	moved, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("cash-guard-%s-withdraw-%d", name, cashID), CashID: cashID, InvType: inv, TargetSlot: 3})
	if err != nil {
		t.Fatal(err)
	}
	cs.Session.Character = moved.Character
	if _, err = s.BeginCashReturn(ctx, cs, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	a, handled, err := s.ConsumeCashReturn(ctx, handle, a.Lease.WorldID, 1, "127.0.0.1", a.Lease)
	if err != nil || !handled {
		t.Fatalf("real cash return handled=%v err=%v", handled, err)
	}
	definition, err := inventoryDefinition(template)
	if err != nil || definition.Cash || !definition.Droppable() {
		t.Fatalf("ordinary template counterexample lost: cash=%v droppable=%v err=%v", definition.Cash, definition.Droppable(), err)
	}
	var source domain.TradeItem
	var actualCash int64
	if inv == 1 {
		e := equipmentAt(a.Character, 3)
		source = domain.TradeItem{InvType: 1, Slot: 3, Quantity: 1, InstanceID: e.ID, TradeSlot: 2}
		actualCash = e.CashID
	} else {
		for _, item := range a.Character.Items {
			if item.InvType == 2 && item.Slot == 3 {
				source = domain.TradeItem{InvType: 2, Slot: 3, Quantity: item.Quantity, InstanceID: item.ID, TradeSlot: 2}
				actualCash = item.CashID
			}
		}
	}
	if source.InstanceID == 0 || actualCash != cashID || cashID <= 0 {
		t.Fatalf("cash move did not persist native serial: instance=%d serial=%d want=%d", source.InstanceID, actualCash, cashID)
	}
	t.Logf("real cash move proof: template=%d inv=%d CashID=%d physical_instance=%d quantity=%d definition.Cash=false Droppable=true", template, inv, actualCash, source.InstanceID, quantity)
	return ctx, s, a, b, cashID, source
}

// With trade_cash_items on, a cash serial changes hands with its item: the
// whole stack and the equipment keep their serial, part of a stack gets a
// serial of its own, and nothing is left behind for the giver's locker.
func TestCashSerialTradeWhenAllowed(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		template, inv, quantity, offered int
	}{
		{"bundle", 2000000, 2, 2, 2},
		{"split", 2000000, 2, 5, 2},
		{"equipment", 1302000, 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer domain.SetTradeCashItemsForTest(true)()
			ctx, s, a, b, cashID, source := cashSerialFixture(t, tc.name, tc.template, tc.inv, tc.quantity)
			source.Quantity = tc.offered
			// The fixture clears the giver's serials; the receiver's are new here.
			t.Cleanup(func() {
				_, _ = s.db.ExecContext(context.Background(), `DELETE FROM asset_cash_moves WHERE cash_id IN(SELECT id FROM asset_cash_items WHERE account_id=$1)`, b.AccountID)
				_, _ = s.db.ExecContext(context.Background(), `DELETE FROM asset_cash_items WHERE account_id=$1`, b.AccountID)
			})
			var err error
			if a.Character, err = s.GetCharacter(ctx, a.CharacterID); err != nil {
				t.Fatal(err)
			}
			if b.Character, err = s.GetCharacter(ctx, b.CharacterID); err != nil {
				t.Fatal(err)
			}
			request := domain.TradeSettlement{ID: fmt.Sprintf("trade-cash-allowed-%s-%d-%d", tc.name, a.CharacterID, b.CharacterID), First: a, Second: b, FirstOffer: domain.TradeOffer{Items: []domain.TradeItem{source}}}
			if _, err = s.SettleTrade(ctx, request); err != nil {
				t.Fatal(err)
			}
			if replay, e := s.SettleTrade(ctx, request); e != nil || !replay.Replayed {
				t.Fatalf("replay=%v err=%v", replay.Replayed, e)
			}
			giver, err := s.GetCharacter(ctx, a.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			receiver, err := s.GetCharacter(ctx, b.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			var received, receivedQuantity, kept, keptQuantity = int64(0), 0, int64(0), 0
			for _, e := range receiver.Equipment {
				if e.CashID > 0 {
					received, receivedQuantity = e.CashID, 1
				}
			}
			for _, item := range receiver.Items {
				if item.CashID > 0 {
					received, receivedQuantity = item.CashID, item.Quantity
				}
			}
			for _, e := range giver.Equipment {
				if e.CashID > 0 {
					kept, keptQuantity = e.CashID, 1
				}
			}
			for _, item := range giver.Items {
				if item.CashID > 0 {
					kept, keptQuantity = item.CashID, item.Quantity
				}
			}
			left := tc.quantity - tc.offered
			if received == 0 || receivedQuantity != tc.offered || keptQuantity != left {
				t.Fatalf("received serial=%d quantity=%d, giver keeps serial=%d quantity=%d", received, receivedQuantity, kept, keptQuantity)
			}
			if left == 0 && (received != cashID || kept != 0) {
				t.Fatalf("whole item: serial %d became %d, giver still has %d", cashID, received, kept)
			}
			if left > 0 && (kept != cashID || received == cashID) {
				t.Fatalf("split: giver keeps %d want %d, receiver has %d", kept, cashID, received)
			}
			rows, err := s.db.QueryContext(ctx, `SELECT id,account_id,COALESCE(character_id,0),quantity,state FROM asset_cash_items WHERE account_id IN($1,$2) ORDER BY id`, a.AccountID, b.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			total, count := 0, 0
			for rows.Next() {
				var id, account, character int64
				var quantity int
				var state string
				if err = rows.Scan(&id, &account, &character, &quantity, &state); err != nil {
					t.Fatal(err)
				}
				owner, holds := a, keptQuantity
				if id == received {
					owner, holds = b, receivedQuantity
				}
				if state != "inventory" || account != owner.AccountID || character != owner.CharacterID || quantity != holds {
					t.Fatalf("serial %d: account=%d character=%d quantity=%d state=%s", id, account, character, quantity, state)
				}
				total += quantity
				count++
			}
			if err = rows.Err(); err != nil || total != tc.quantity || count != 1+min(left, 1) {
				t.Fatalf("serials=%d total quantity=%d want %d err=%v", count, total, tc.quantity, err)
			}
		})
	}
}
