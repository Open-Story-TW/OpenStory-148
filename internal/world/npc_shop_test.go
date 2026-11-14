package world

import (
	"context"
	"testing"

	"maple-go-server/internal/domain"
)

func TestNativeShopRequiresCurrentNearbyNPC(t *testing.T) {
	gameplay, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	room, err := gameplay.room(mapKey{channel: 1, mapID: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, MapID: 1000000}}}
	var oid int32
	for id, npc := range room.npcs {
		if npc.TemplateID == 11000 {
			oid = id
			live.session.Character.X = npc.X + 1000
			live.session.Character.Y = npc.Y
		}
	}
	if oid == 0 {
		t.Fatal("source Maple Island merchant is absent")
	}
	s := &Server{gameplay: gameplay, economyDialogs: newEconomyDialogSpace()}
	sink := &memSink{}
	if handled, err := s.openNativeNPCShop(context.Background(), 1, live, sink, oid); !handled || err != nil || s.getEconomyDialog(live, "shop") != nil {
		t.Fatal("out-of-range shop opened")
	}
	live.session.Character.X -= 1000
	if handled, err := s.openNativeNPCShop(context.Background(), 1, live, sink, oid); !handled || err != nil {
		t.Fatalf("nearby native shop: %v %v", handled, err)
	}
	dialog := s.getEconomyDialog(live, "shop")
	if dialog == nil || dialog.shop.ID != 1 || dialog.npcID != 11000 || len(dialog.shop.Items) == 0 {
		t.Fatal("source NPC did not open its authoritative shop")
	}
}
