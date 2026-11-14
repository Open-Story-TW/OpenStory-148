package persistence

import (
	"encoding/json"
	"testing"

	"maple-go-server/internal/domain"
)

func itemUseReceiptUnit() (domain.UseItemRequest, domain.ItemUseResult) {
	c := domain.Character{ID: 7, AccountID: 8, WorldID: 1, MapID: 10000, HP: 5, MP: 1, MaxHP: 500, MaxMP: 100, Revision: 2, Items: []domain.InventoryItem{{ID: 9, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 2, Revision: 1}}}
	q := domain.UseItemRequest{Session: domain.CharacterSession{AccountID: 8, CharacterID: 7, SessionID: "item", Generation: 3, ChannelID: 1, Lease: domain.WorldLease{WorldID: 1}, Character: c}, ClientTick: 99, Slot: 1, TemplateID: 2000000}
	c.Items = append([]domain.InventoryItem(nil), c.Items...)
	c.Items[0].Quantity--
	c.Items[0].Revision++
	c.Revision++
	c.HP = 175
	c.MP = 31
	r := domain.ItemUseResult{Character: c, SessionID: "item", Generation: 3, ChannelID: 1, ClientTick: 99, Slot: 1, TemplateID: 2000000, ItemInstanceID: 9, QuantityAfter: 1, CommittedRevision: 3, HPBefore: 5, MPBefore: 1, FlatHP: 50, FlatMP: 20, HPRate: 20, MPRate: 10, FixedHPPercent: 40}
	return q, r
}

func TestItemUseReceiptCanonicalQuantityVitalsAndJSONRoundtrip(t *testing.T) {
	q, r := itemUseReceiptUnit()
	if err := validateItemUseReceipt(q, r); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	var after domain.ItemUseResult
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(after)
	if itemUseDigest(raw) != itemUseDigest(again) || validateItemUseReceipt(q, after) != nil {
		t.Fatal("immutable receipt bytes changed")
	}
	for name, change := range map[string]func(*domain.ItemUseResult){
		"slot": func(r *domain.ItemUseResult) { r.Slot++ }, "tick": func(r *domain.ItemUseResult) { r.ClientTick++ }, "session": func(r *domain.ItemUseResult) { r.SessionID = "other" },
		"generation": func(r *domain.ItemUseResult) { r.Generation++ }, "channel": func(r *domain.ItemUseResult) { r.ChannelID++ }, "account": func(r *domain.ItemUseResult) { r.Character.AccountID++ },
		"revision": func(r *domain.ItemUseResult) { r.CommittedRevision++ }, "quantity": func(r *domain.ItemUseResult) { r.QuantityAfter = 99 }, "missing stack": func(r *domain.ItemUseResult) { r.Character.Items = nil },
		"duplicate stack": func(r *domain.ItemUseResult) { r.Character.Items = append(r.Character.Items, r.Character.Items[0]) }, "foreign instance": func(r *domain.ItemUseResult) { r.ItemInstanceID++ },
		"hp": func(r *domain.ItemUseResult) { r.Character.HP++ }, "mp": func(r *domain.ItemUseResult) { r.Character.MP++ }, "wrong percentage source": func(r *domain.ItemUseResult) { r.FixedHPPercent = 50 },
		"unscaled flat": func(r *domain.ItemUseResult) { r.Character.HP = 155 }, "replay persisted": func(r *domain.ItemUseResult) { r.Replayed = true },
	} {
		t.Run(name, func(t *testing.T) {
			_, bad := itemUseReceiptUnit()
			change(&bad)
			if err := validateItemUseReceipt(q, bad); err == nil {
				t.Fatal("corrupt item receipt accepted", name)
			}
		})
	}
}
