package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestNPCShopAuthorityReplayBuySell(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	shop, err := content.NPCShop(2)
	if err != nil {
		t.Fatal(err)
	}
	var red domain.ShopItem
	for _, item := range shop.Items {
		if item.TemplateID == 2000000 {
			red = item
			break
		}
	}
	if red.Price != 5 {
		t.Fatalf("legacy catalog price=%d", red.Price)
	}
	req := domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-buy-operation-%d", a.CharacterID), ShopID: 2, Kind: "buy", EntryID: red.ID, TemplateID: 2000000, Quantity: 2}
	result, err := s.ChangeShop(ctx, req)
	if err != nil || result.Inventory.Character.Mesos != 990 || result.Inventory.Character.Items[0].Quantity != 7 {
		t.Fatalf("buy=%+v %v", result, err)
	}
	replay, err := s.ChangeShop(ctx, req)
	if err != nil || !replay.Replayed || replay.Inventory.Character.Mesos != 990 {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	wrong := req
	wrong.Quantity = 3
	if _, err = s.ChangeShop(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay=%v", err)
	}
	a.Character = result.Inventory.Character
	wrong = req
	wrong.Session = a
	wrong.OperationID += "-forged"
	wrong.TemplateID = 2000001
	if _, err = s.ChangeShop(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forged template=%v", err)
	}
	result, err = s.ChangeShop(ctx, domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-sell-operation-%d", a.CharacterID), ShopID: 2, Kind: "sell", Slot: 1, TemplateID: 2000000, Quantity: 2})
	if err != nil || result.Inventory.Character.Mesos != 996 || result.Inventory.Character.Items[0].Quantity != 5 {
		t.Fatalf("sell=%+v %v", result, err)
	}
	a.Character = result.Inventory.Character
	weaponShop, err := content.NPCShop(1)
	if err != nil {
		t.Fatal(err)
	}
	weapon := weaponShop.Items[0]
	result, err = s.ChangeShop(ctx, domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-weapon-operation-%d", a.CharacterID), ShopID: 1, Kind: "buy", EntryID: weapon.ID, TemplateID: weapon.TemplateID, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := equipmentAt(result.Inventory.Character, 1)
	if e.ID <= 0 || e.ItemID != 1302000 || e.WATK != 17 {
		t.Fatalf("equipment purchase=%+v", e)
	}
}

func TestNPCShopFullInventoryRollback(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_items SET quantity=300 WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	for slot := 2; slot <= 24; slot++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,2000000,300)`, a.CharacterID, slot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ChangeShop(ctx, domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-full-operation-%d", a.CharacterID), ShopID: 2, Kind: "buy", EntryID: 5, TemplateID: 2000000, Quantity: 1}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full bag=%v", err)
	}
	c, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || c.Mesos != 1000 || c.Revision != a.Character.Revision || len(c.Items) != 24 {
		t.Fatalf("rollback=%+v %v", c, err)
	}
}

func TestNPCShopRechargeFractionalPrice(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	root := t.TempDir()
	path := filepath.Join(root, "Item.wz", "Consume")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="0207.img"><imgdir name="02070000"><imgdir name="info"><int name="slotMax" value="500"/><int name="price" value="250"/><double name="unitPrice" value="0.5"/></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(path, "0207.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,2,2070000,6)`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	result, err := s.ChangeShop(ctx, domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-recharge-operation-%d", a.CharacterID), ShopID: 2, Kind: "recharge", Slot: 2, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inventory.Character.Mesos != 753 || result.Inventory.Character.Items[1].Quantity != 500 {
		t.Fatalf("recharge=%+v", result)
	}
	a.Character = result.Inventory.Character
	result, err = s.ChangeShop(ctx, domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("npcshop-ammo-sell-operation-%d", a.CharacterID), ShopID: 2, Kind: "sell", Slot: 2, TemplateID: 2070000, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inventory.Character.Mesos != 1003 || len(result.Inventory.Character.Items) != 1 {
		t.Fatalf("ammo whole-stack sale=%+v", result)
	}
}
