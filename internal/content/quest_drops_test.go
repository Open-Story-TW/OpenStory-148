package content

import (
	"maple-go-server/internal/domain"
	"testing"
)

func TestQuestDropNeedsActiveQuestAndMissingRequiredItem(t *testing.T) {
	c := domain.Character{Quests: []domain.QuestState{{ID: 1035, State: 1}}}
	if remaining, err := QuestDropRemaining("", c, 1035, 4031802); err != nil || remaining != 1 {
		t.Fatalf("required item: %d %v", remaining, err)
	}
	c.Items = []domain.InventoryItem{{TemplateID: 4031802, InvType: 4, Quantity: 1}}
	if remaining, err := QuestDropRemaining("", c, 1035, 4031802); err != nil || remaining != 0 {
		t.Fatal("already collected item remained eligible")
	}
	for _, state := range []int{0, 2} {
		c.Quests[0].State = state
		c.Items = nil
		if remaining, err := QuestDropRemaining("", c, 1035, 4031802); err != nil || remaining != 0 {
			t.Fatal("inactive quest exposed reward")
		}
	}
	c.Quests[0].State = 1
	if remaining, err := QuestDropRemaining("", c, 1035, 4000000); err != nil || remaining != 0 {
		t.Fatal("unrequired item authorized")
	}
}
