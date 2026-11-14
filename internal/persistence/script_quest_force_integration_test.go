package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func questStateFor(states []domain.QuestState, id int) domain.QuestState {
	for _, q := range states {
		if q.ID == id {
			return q
		}
	}
	return domain.QuestState{ID: id}
}

func TestScriptExchangeForcesQuestStatusWithRewards(t *testing.T) {
	ctx, store, sessions := inventoryFixture(t)
	actor := sessions[0]
	level, exp := actor.Character.Level, actor.Character.EXP
	note := "3"
	request := domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("quest-force-%d-reward", actor.CharacterID),
		EXP:   7,
		Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 2}},
		Quests: []domain.ScriptQuestForce{
			{QuestID: 8248, State: 1}, {QuestID: 8248, State: 2},
			{QuestID: 1406, State: 1, Data: &note},
		}}
	first, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	done, record := questStateFor(first.Character.Quests, 8248), questStateFor(first.Character.Quests, 1406)
	if done.State != 2 || done.CompletedAt.IsZero() || record.State != 1 || len(first.Quests) != 2 {
		t.Fatalf("forced states: %+v %+v %+v", done, record, first.Quests)
	}
	if first.Character.Level == level && first.Character.EXP != exp+7 {
		t.Fatalf("experience not applied: level %d exp %d", first.Character.Level, first.Character.EXP)
	}
	actor.Character = first.Character
	states, err := store.QuestStates(ctx, actor)
	if err != nil || questStateFor(states, 8248).State != 2 || questStateFor(states, 1406).State != 1 {
		t.Fatalf("forced states not durable: %+v %v", states, err)
	}
	data, err := store.ReadScriptQuestData(ctx, actor, 1406)
	if err != nil || data.Data != "3" {
		t.Fatalf("forced start data: %+v %v", data, err)
	}
	if first.Character.QuestRecords[1406] != "3" {
		t.Fatalf("record missing from the committed character: %+v", first.Character.QuestRecords)
	}
	if loaded, err := store.GetCharacter(ctx, actor.CharacterID); err != nil || loaded.QuestRecords[1406] != "3" {
		t.Fatalf("record missing from a reloaded character: %+v %v", loaded.QuestRecords, err)
	}
	replayed, err := store.ExecuteScriptExchange(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Character.EXP != first.Character.EXP || replayed.Character.Level != first.Character.Level {
		t.Fatalf("replay granted again: %+v %v", replayed, err)
	}
	// A reward that cannot be paid for leaves the quest untouched.
	rollback := domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("quest-force-%d-rollback", actor.CharacterID),
		Items:  []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -30000}},
		Quests: []domain.ScriptQuestForce{{QuestID: 2186, State: 2}}}
	if _, err = store.ExecuteScriptExchange(ctx, rollback); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("missing materials accepted: %v", err)
	}
	if states, err = store.QuestStates(ctx, actor); err != nil || questStateFor(states, 2186).State != 0 {
		t.Fatalf("failed exchange changed a quest: %+v %v", states, err)
	}
	invalid := domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("quest-force-%d-invalid", actor.CharacterID),
		Quests: []domain.ScriptQuestForce{{QuestID: 2186, State: 3}}}
	if _, err = store.ExecuteScriptExchange(ctx, invalid); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid forced state accepted: %v", err)
	}
}
