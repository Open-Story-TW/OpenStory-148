package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestScriptQuestDataSharesRewardTransaction(t *testing.T) {
	ctx, store, sessions := inventoryFixture(t)
	actor, other := sessions[0], sessions[1]
	const questID = 700001
	empty := ""
	before, err := store.ReadScriptQuestData(ctx, actor, questID)
	if err != nil || before.Data != "" || before.Revision != 0 {
		t.Fatalf("missing record: %+v %v", before, err)
	}
	request := domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("quest-record-%d-reward", actor.CharacterID),
		QuestData:  []domain.ScriptQuestChange{{QuestID: questID, Data: "rewarded:page2", Expected: &empty}},
		MesosDelta: 75, Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 1}}}
	first, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil || first.Character.Mesos != 1075 || first.QuestData[0].Data != "rewarded:page2" {
		t.Fatalf("record/reward commit: %+v %v", first, err)
	}
	actor.Character = first.Character
	reloaded, err := store.ReadScriptQuestData(ctx, actor, questID)
	if err != nil || reloaded.Data != "rewarded:page2" {
		t.Fatalf("durable dialogue record: %+v %v", reloaded, err)
	}
	isolated, err := store.ReadScriptQuestData(ctx, other, questID)
	if err != nil || isolated.Data != "" || isolated.Revision != 0 {
		t.Fatalf("another character inherited dialogue data: %+v %v", isolated, err)
	}
	replayed, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Character.Mesos != 1075 {
		t.Fatalf("lost reply duplicated reward: %+v %v", replayed, err)
	}
	retry := request
	retry.Session = actor
	retry.OperationID += "-new-session-command"
	if _, err = store.ExecuteScriptExchange(ctx, retry); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("stored progress did not prevent repeat grant: %v", err)
	}
	altered := request
	altered.QuestData = []domain.ScriptQuestChange{{QuestID: questID, Data: "tampered"}}
	if _, err = store.ExecuteScriptExchange(ctx, altered); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	expected := "rewarded:page2"
	rollback := domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("quest-record-%d-rollback", actor.CharacterID),
		QuestData: []domain.ScriptQuestChange{{QuestID: questID, Data: "next-page", Expected: &expected}},
		Items:     []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -100}}}
	if _, err = store.ExecuteScriptExchange(ctx, rollback); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("missing materials accepted: %v", err)
	}
	final, err := store.ReadScriptQuestData(ctx, actor, questID)
	if err != nil || final != reloaded {
		t.Fatalf("failed reward advanced dialogue: %+v %v", final, err)
	}
	quests, err := store.QuestStates(ctx, actor)
	if err != nil || len(quests) != 0 {
		t.Fatalf("custom record started an unrelated quest: %+v %v", quests, err)
	}
}
