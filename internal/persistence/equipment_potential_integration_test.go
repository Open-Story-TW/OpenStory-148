package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

// Fixture-only seeding exercises preservation before any potential-generating
// runtime command exists. It is not an original-client potential acquisition.
func seedPotentialEquipment(t *testing.T, ctx context.Context, s *Store, a *domain.CharacterSession, p domain.EquipmentPotential, enhance uint8) domain.Equipment {
	t.Helper()
	e := equipmentAt(a.Character, -11)
	e.Potential, e.Enhance = p, enhance
	raw, err := json.Marshal(equipmentMetadata{Potential: p, Enhance: enhance})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_equipment_metadata(equipment_id,account_id,metadata) VALUES($1,$2,$3) ON CONFLICT(equipment_id) DO UPDATE SET metadata=EXCLUDED.metadata`, e.ID, a.AccountID, raw); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func assertPotentialEquipment(t *testing.T, got, want domain.Equipment) {
	t.Helper()
	if got.ItemID != want.ItemID || got.Potential != want.Potential || got.Enhance != want.Enhance {
		t.Fatalf("equipment metadata lost: got=%+v want=%+v", got, want)
	}
}

func assertPotentialReaders(t *testing.T, ctx context.Context, s *Store, cid int64, slot int, want domain.Equipment) {
	t.Helper()
	// GetCharacter uses Ent; asset transactions use the separate SQL loader.
	for _, sql := range []bool{false, true} {
		var c domain.Character
		var err error
		if sql {
			c, err = readCharacterSQL(ctx, s.db, cid)
		} else {
			c, err = s.GetCharacter(ctx, cid)
		}
		if err != nil {
			t.Fatal(err)
		}
		assertPotentialEquipment(t, equipmentAt(c, slot), want)
	}
}

func TestPotentialEquipmentScriptStorageDropTradeAndCashDurability(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	want := seedPotentialEquipment(t, ctx, s, &a, domain.EquipmentPotential{Present: true, RawState: 1, Options: [5]uint16{0xffef, 0xffec, 0xffff, 7, 30123}, Flags: 0xffff}, 253)
	assertPotentialReaders(t, ctx, s, a.CharacterID, -11, want)
	// Bundle-only scripts still commit the full aggregate. ScrollLevel/Flags
	// are deliberately zero: these must not delete potential-only metadata.
	r := domain.ScriptExchange{Session: a, OperationID: fmt.Sprintf("potential-script-preserve-%d", a.CharacterID), Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 1}}}
	script, err := s.ExecuteScriptExchange(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = script.Character
	assertPotentialEquipment(t, equipmentAt(a.Character, -11), want)
	prior, err := s.ExecuteScriptExchange(ctx, r)
	if err != nil || !prior.Replayed {
		t.Fatalf("script receipt replay=%+v %v", prior, err)
	}
	assertPotentialEquipment(t, equipmentAt(prior.Character, -11), want)
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 41001, InvType: 1, Source: -11, Destination: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = moved.Character
	assertPotentialEquipment(t, equipmentAt(moved.Character, 1), want)
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("potential-storage-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: want.ItemID, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposit.Inventory.Character
	stored, err := s.OpenStorage(ctx, a)
	if err != nil || len(stored.Items) != 1 {
		t.Fatalf("reopened storage=%+v %v", stored, err)
	}
	assertPotentialEquipment(t, *stored.Items[0].Equipment, want)
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("potential-storage-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: stored.Items[0].ID, ExpectedRevision: stored.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	drop, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 41002, InvType: 1, Source: 1, Destination: 0, Quantity: 1})
	if err != nil || drop.Drop == nil {
		t.Fatalf("drop=%+v %v", drop, err)
	}
	a.Character = drop.Character
	listed, err := s.ListItemDrops(ctx, a.Character.WorldID, a.ChannelID, a.Character.MapID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range listed {
		if d.ObjectID == drop.Drop.ObjectID && d.Equipment != nil {
			assertPotentialEquipment(t, *d.Equipment, want)
			found = true
		}
	}
	if !found {
		t.Fatal("durable equipment drop missing")
	}
	picked, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: b, ObjectID: drop.Drop.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	b.Character = picked.Character
	assertPotentialReaders(t, ctx, s, b.CharacterID, 1, want)
	tradeReq := domain.TradeSettlement{ID: fmt.Sprintf("potential-trade-return-%d", want.ID), First: b, Second: a, FirstOffer: domain.TradeOffer{Items: []domain.TradeItem{{InvType: 1, Slot: 1, Quantity: 1, InstanceID: want.ID, TradeSlot: 1}}}}
	trade, err := s.SettleTrade(ctx, tradeReq)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = trade.Second.Character
	assertPotentialReaders(t, ctx, s, a.CharacterID, 1, want)
	replayed, err := s.SettleTrade(ctx, tradeReq)
	if err != nil || !replayed.Replayed {
		t.Fatalf("trade receipt=%+v %v", replayed, err)
	}
	assertPotentialEquipment(t, equipmentAt(replayed.Second.Character, 1), want)
	// Attach a cash serial only in this persistence fixture. This does not
	// enable potential actions on cash equipment or relax native trade rules.
	var cashID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,state) VALUES($1,$2,$3,1,$4,1,1,1,'inventory') RETURNING id`, a.AccountID, a.CharacterID, a.Lease.WorldID, want.ItemID).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	if _, err = s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	for i, toLocker := range []bool{true, false, true, false} {
		req := domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("potential-cash-preserve-%d-%d", want.ID, i), CashID: cashID, ToLocker: toLocker}
		result, err := s.MoveCashItem(ctx, req)
		if err != nil || result.Item.Equipment == nil {
			t.Fatalf("cash move=%+v %v", result, err)
		}
		assertPotentialEquipment(t, *result.Item.Equipment, want)
		cs.Session.Character = result.Character
		if !toLocker {
			e := equipmentAt(result.Character, 1)
			if e.ID == want.ID || e.CashID != cashID {
				t.Fatalf("cash ID churn fixture missing: %+v", e)
			}
			assertPotentialReaders(t, ctx, s, a.CharacterID, 1, want)
		}
	}
}

func TestPotentialEquipmentExplicitZeroRollbackAndQAFamilyPreservation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	want := seedPotentialEquipment(t, ctx, s, &a, domain.EquipmentPotential{Present: true}, 0)
	// An unrelated commit must retain a row even if every raw wire field is 0.
	r, err := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: a, MesosDelta: 1, OperationID: fmt.Sprintf("potential-zero-preserve-%d", a.CharacterID)})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = r.Character
	assertPotentialReaders(t, ctx, s, a.CharacterID, -11, want)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, a)
	if err != nil {
		t.Fatal(err)
	}
	gear, ok := qaFamilyGear(0, c.Gender)
	if !ok {
		t.Fatal("QA warrior fixture unavailable")
	}
	if err = swapFamilyGear(ctx, tx, c, gear); err != nil {
		t.Fatal(err)
	}
	c, err = readCharacterSQL(ctx, tx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldFound := false
	for _, e := range c.Equipment {
		if e.ID == want.ID {
			oldFound = e.Slot > 0
			assertPotentialEquipment(t, e, want)
		} else if e.Slot == -11 && !e.Potential.IsZero() {
			t.Fatal("new QA starter gear inherited old potential")
		}
	}
	if !oldFound {
		t.Fatal("QA family deleted the previous equipment")
	}
	for i := range c.Equipment {
		if c.Equipment[i].ID == want.ID {
			c.Equipment[i].Potential = domain.EquipmentPotential{Present: true, RawState: 255, Flags: 65535}
		}
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertPotentialReaders(t, ctx, s, a.CharacterID, -11, want)
}

func TestPotentialEquipmentUnknownMetadataBlocksCommandWithoutLoss(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	e := equipmentAt(a.Character, -11)
	for i, raw := range []string{
		`{"scroll_level":0,"flags":0,"future_enhance":{"preserve_me":7}}`,
		`{"potential":{"present":true,"raw_state":1,"options":[65519,0,0,0,0],"future_option":9}}`,
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_equipment_metadata(equipment_id,account_id,metadata) VALUES($1,$2,$3) ON CONFLICT(equipment_id) DO UPDATE SET metadata=EXCLUDED.metadata`, e.ID, a.AccountID, raw); err != nil {
			t.Fatal(err)
		}
		op := fmt.Sprintf("potential-unknown-preserve-%d-%d", a.CharacterID, i)
		_, err := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: a, OperationID: op, MesosDelta: 10, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -1}}})
		if err == nil {
			t.Fatal("unknown metadata was accepted and would be stripped by remarshal")
		}
		var unchanged bool
		if err = s.db.QueryRowContext(ctx, `SELECT metadata=$2::jsonb FROM asset_equipment_metadata WHERE equipment_id=$1`, e.ID, raw).Scan(&unchanged); err != nil || !unchanged {
			t.Fatalf("unknown metadata lost: same=%v err=%v", unchanged, err)
		}
		var mesos, revision int64
		if err = s.db.QueryRowContext(ctx, `SELECT mesos,revision FROM game_characters WHERE id=$1`, a.CharacterID).Scan(&mesos, &revision); err != nil || mesos != a.Character.Mesos || revision != a.Character.Revision {
			t.Fatalf("rejected metadata changed progress: mesos=%d revision=%d err=%v", mesos, revision, err)
		}
		var quantity, receipts int
		if err = s.db.QueryRowContext(ctx, `SELECT quantity FROM asset_items WHERE character_id=$1 AND inv_type=2 AND slot=1`, a.CharacterID).Scan(&quantity); err != nil || quantity != 5 {
			t.Fatalf("rejected metadata consumed material: quantity=%d err=%v", quantity, err)
		}
		if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_script_commands WHERE character_id=$1 AND operation_id=$2`, a.CharacterID, op).Scan(&receipts); err != nil || receipts != 0 {
			t.Fatalf("rejected metadata recorded success: count=%d err=%v", receipts, err)
		}
	}
}
