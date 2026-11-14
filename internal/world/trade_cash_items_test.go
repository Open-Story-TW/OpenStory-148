package world

import (
	"testing"

	"maple-go-server/internal/domain"
)

// The trade window takes a cash serial only under trade_cash_items, and a
// cash stack only once.
func TestBuildTradeItemCashSerial(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	c := domain.Character{
		Equipment: []domain.Equipment{{ID: 11, ItemID: 1302000, Slot: 1, CashID: 5}},
		Items:     []domain.InventoryItem{{ID: 21, InvType: 2, Slot: 1, TemplateID: 2000000, Quantity: 5, CashID: 6}, {ID: 22, InvType: 2, Slot: 2, TemplateID: 2000000, Quantity: 5}, {ID: 23, InvType: 5, Slot: 1, TemplateID: 2000000, Quantity: 3, CashID: 7}},
	}
	if c.Capacity(1) < 1 || c.Capacity(2) < 2 || c.Capacity(5) < 1 {
		t.Fatal("fixture has no inventory slots")
	}
	if _, ok := buildTradeItem(c, domain.TradeOffer{}, 1, 1, 1, 1); ok {
		t.Fatal("cash equipment offered with the setting off")
	}
	if _, ok := buildTradeItem(c, domain.TradeOffer{}, 2, 1, 2, 1); ok {
		t.Fatal("cash stack offered with the setting off")
	}
	if _, ok := buildTradeItem(c, domain.TradeOffer{}, 5, 1, 1, 1); ok {
		t.Fatal("cash tab stack offered with the setting off")
	}
	if _, ok := buildTradeItem(c, domain.TradeOffer{}, 2, 2, 2, 1); !ok {
		t.Fatal("ordinary stack refused")
	}
	defer domain.SetTradeCashItemsForTest(true)()
	if item, ok := buildTradeItem(c, domain.TradeOffer{}, 1, 1, 1, 1); !ok || item.Equipment.CashID != 5 {
		t.Fatalf("cash equipment: %+v %v", item, ok)
	}
	first, ok := buildTradeItem(c, domain.TradeOffer{}, 2, 1, 2, 1)
	if !ok || first.Item.CashID != 6 {
		t.Fatalf("cash stack: %+v %v", first, ok)
	}
	if _, ok = buildTradeItem(c, domain.TradeOffer{Items: []domain.TradeItem{first}}, 2, 1, 1, 2); ok {
		t.Fatal("the same cash stack offered twice")
	}
	// The launcher asks how many of a cash tab stack to give.
	if tab, ok := buildTradeItem(c, domain.TradeOffer{}, 5, 1, 2, 1); !ok || tab.Quantity != 2 || tab.Item.CashID != 7 {
		t.Fatalf("cash tab stack: %+v %v", tab, ok)
	}
}
