package persistence

import (
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

func TestScriptEquipmentExchangeUsesBagAndRollsBack(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	const base, reward, token = 1322005, 1302000, 2000000

	reload := func() domain.Character {
		t.Helper()
		value, err := store.GetCharacter(ctx, actor.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	exchange := func(operation string, quantity int) domain.ScriptExchange {
		actor.Character = reload()
		return domain.ScriptExchange{
			Session: actor, OperationID: operation,
			Items: []domain.ScriptItemDelta{
				{TemplateID: token, Quantity: -1},
				{TemplateID: base, Quantity: -1},
				{TemplateID: reward, Quantity: quantity},
			},
		}
	}
	equipmentAt := func(c domain.Character, slot int) int {
		for _, e := range c.Equipment {
			if e.Slot == slot {
				return e.ItemID
			}
		}
		return 0
	}
	if _, err := store.ExecuteScriptExchange(ctx, exchange("script-equip-worn", 1)); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("worn equipment was accepted: %v", err)
	}
	if c := reload(); equipmentAt(c, -11) != base || itemQuantity(c, token) != 5 {
		t.Fatal("missing bag equipment partially consumed the exchange")
	}

	if _, err := store.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot)
		SELECT $1, CASE WHEN n IN (2,4) THEN $2 ELSE 1312004 END, n FROM generate_series(1,$3::int) n`,
		actor.CharacterID, base, actor.Character.Capacity(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecuteScriptExchange(ctx, exchange("script-equip-full", 2)); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("second reward fit in a full bag: %v", err)
	}
	if c := reload(); equipmentAt(c, 2) != base || equipmentAt(c, 4) != base || itemQuantity(c, token) != 5 {
		t.Fatal("capacity failure partially consumed the input equipment or currency")
	}

	request := exchange("script-equip-replace", 1)
	got, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if equipmentAt(got.Character, 2) != reward || equipmentAt(got.Character, 4) != base ||
		equipmentAt(got.Character, -11) != base || itemQuantity(got.Character, token) != 4 {
		t.Fatal("exchange did not replace the lowest matching bag slot")
	}
	replay, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil || !replay.Replayed || itemQuantity(replay.Character, token) != 4 {
		t.Fatalf("committed equipment exchange did not replay safely: %v", err)
	}
}
