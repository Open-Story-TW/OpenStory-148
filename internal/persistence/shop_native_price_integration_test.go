package persistence

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"reflect"
	"testing"
)

func TestNativeShopDisplayedPriceAuthorityAndReplay(t *testing.T) {
	useCombatSchema(t)
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	shop, err := content.NPCShop(2)
	if err != nil {
		t.Fatal(err)
	}
	var red domain.ShopItem
	var index int
	for n, item := range shop.Items {
		if item.TemplateID == 2000000 {
			red = item
			index = n
			break
		}
	}
	if red.Price != 5 {
		t.Fatalf("catalog price%d", red.Price)
	}
	// Decode the exact independently audited native shape, including its price.
	wire, _ := hex.DecodeString("00000080841e00020005000000")
	wire[1] = byte(index)
	wire[2] = byte(index >> 8)
	input, err := protocol.ReadNPCShopRequest(protocol.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	req := domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("native-shop-priced-%d", a.CharacterID), ShopID: 2, Kind: "buy", EntryID: red.ID, TemplateID: input.TemplateID, Quantity: input.Quantity, DisplayedUnitPrice: &input.DisplayedUnitPrice}
	before, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for _, price := range []int64{-1, 0, 1, 4, 6, 2147483647} {
		bad := req
		bad.OperationID = fmt.Sprintf("native-shop-forged-%d-%d", a.CharacterID, price)
		bad.DisplayedUnitPrice = &price
		if _, err = s.ChangeShop(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("client price%d err%v", price, err)
		}
		after, e := s.GetCharacter(ctx, a.CharacterID)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("forged displayed price mutated assets%+v err%v", after, e)
		}
		var receipts int
		if e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_shop_operations WHERE operation_id=$1`, bad.OperationID).Scan(&receipts); e != nil || receipts != 0 {
			t.Fatalf("forged price receipt%d err%v", receipts, e)
		}
	}
	result, err := s.ChangeShop(ctx, req)
	if err != nil || result.Inventory.Character.Mesos != 990 || result.Inventory.Character.Items[0].Quantity != 7 {
		t.Fatalf("native buy%+v err%v", result, err)
	}
	replay, err := s.ChangeShop(ctx, req)
	if err != nil || !replay.Replayed || replay.Inventory.Character.Mesos != 990 || replay.Inventory.Character.Items[0].Quantity != 7 {
		t.Fatalf("native replay%+v err%v", replay, err)
	}
	zero := int64(0)
	changed := req
	changed.DisplayedUnitPrice = &zero
	if _, err = s.ChangeShop(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("same operation changed price%v", err)
	}
	a.Character = result.Inventory.Character
	sell := domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("native-shop-sell-price-%d", a.CharacterID), ShopID: 2, Kind: "sell", Slot: 1, TemplateID: 2000000, Quantity: 2, DisplayedUnitPrice: &zero}
	if _, err = s.ChangeShop(ctx, sell); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("sell price injection%v", err)
	}
	sell.DisplayedUnitPrice = nil
	result, err = s.ChangeShop(ctx, sell)
	if err != nil || result.Inventory.Character.Mesos != 996 || result.Inventory.Character.Items[0].Quantity != 5 {
		t.Fatalf("native sell%+v err%v", result, err)
	}
}

func TestNativeShopExistingEconomyCasesPrivate(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T)
	}{{"buy_sell_and_equipment", TestNPCShopAuthorityReplayBuySell}, {"full_bag_rollback", TestNPCShopFullInventoryRollback}, {"fractional_recharge_and_whole_ammo_sale", TestNPCShopRechargeFractionalPrice}} {
		t.Run(test.name, func(t *testing.T) { useCombatSchema(t); test.run(t) })
	}
}
