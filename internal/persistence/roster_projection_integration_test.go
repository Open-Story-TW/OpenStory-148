package persistence

import (
	"encoding/json"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestAssetAndQuestWritesKeepRosterProjectionLean(t *testing.T) {
	ctx, store, sessions := inventoryFixture(t)
	actor := sessions[0]
	started, err := store.TransitionQuest(ctx, domain.QuestRequest{Session: actor, QuestID: 1031, NPCID: 2101, Action: "start", OperationID: fmt.Sprintf("roster-quest-test-%d", actor.CharacterID)})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = started.Character
	used, err := store.UseItem(ctx, domain.UseItemRequest{Session: actor, ClientTick: 222, Slot: 1, TemplateID: 2000000})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = used
	var raw []byte
	if err = store.db.QueryRowContext(ctx, `SELECT snapshot FROM game_character_roster WHERE character_id=$1`, actor.CharacterID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var view domain.CharacterSummary
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Items)+len(view.Quests)+len(view.Skills)+len(view.SkillCooldowns)+len(view.SkillBuffs) != 0 || len(view.Equipment) != 4 {
		t.Fatalf("projection includes gameplay data: %s", raw)
	}
	before, _ := store.Stats()
	for i := 0; i < 2; i++ {
		list, err := store.ListCharacters(ctx, actor.AccountID, actor.Lease.WorldID)
		if err != nil || len(list) != 1 || len(list[0].Items)+len(list[0].Quests) != 0 {
			t.Fatalf("roster: %+v %v", list, err)
		}
	}
	after, _ := store.Stats()
	if after-before != 1 {
		t.Fatalf("cold+warm roster SQL loads=%d", after-before)
	}
	full, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || len(full.Quests) != 1 || len(full.Items) != 1 || full.Items[0].Quantity != 4 {
		t.Fatalf("projection reduction changed full character: %+v %v", full, err)
	}
}
