package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestQuestDropPacketsPreserveOwnerTypeAndOrdinaryPolicy(t *testing.T) {
	d := domain.ItemDrop{ObjectID: 1000000100, OwnerID: 7, InvType: 4, TemplateID: 4031802, Quantity: 1, QuestID: 1035, QuestOwnerUntil: time.Now().Add(time.Minute).UnixMilli()}
	for _, kind := range []int{0, 1, 2, 3} {
		d.QuestDropType = kind
		p := DropInventoryItem(d)
		if len(p) == 0 || p[16] != byte(kind) {
			t.Fatalf("native owner type %d: %x", kind, p)
		}
	}
	d.QuestDropType = 0
	d.QuestOwnerUntil = 1
	if p := DropInventoryItem(d); p[16] != 2 {
		t.Fatalf("expired protection did not become FFA: %x", p)
	}
	d.QuestID = 0
	d.QuestOwnerUntil = time.Now().Add(time.Minute).UnixMilli()
	if p := DropInventoryItem(d); p[16] != 2 {
		t.Fatal("ordinary public policy changed")
	}
}
