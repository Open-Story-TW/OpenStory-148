package persistence

import (
	"errors"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestQuestRequirements(t *testing.T) {
	q, e := content.QuestDefinition("", 1031)
	if e != nil {
		t.Fatal(e)
	}
	c := domain.Character{Level: 1, Job: 0, HP: 50, MapID: 10000}
	r := domain.QuestRequest{NPCID: 2101}
	now := time.Now()
	if e = questRequirements(c, domain.QuestState{}, q.Start, r, now); e != nil {
		t.Fatal(e)
	}
	r.NPCID = 2100
	if !errors.Is(questRequirements(c, domain.QuestState{}, q.Start, r, now), domain.ErrForbidden) {
		t.Fatal("wrong npc accepted")
	}
	old, _ := content.QuestDefinition("", 1000)
	r.NPCID = old.Start.NPCID
	if e = questRequirements(c, domain.QuestState{}, old.Start, r, now); e == nil {
		t.Fatal("expired event accepted")
	}
	r.NPCID = 2000
	apple, _ := content.QuestDefinition("", 1021)
	r.Script = "quest/1021.lua"
	c.Items = []domain.InventoryItem{{TemplateID: 2010007, Quantity: 1}}
	if e = questRequirements(c, domain.QuestState{}, apple.Complete, r, now); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("apple not consumed: %v", e)
	}
}
