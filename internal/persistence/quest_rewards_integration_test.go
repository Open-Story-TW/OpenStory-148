package persistence

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestWeightedQuestCompletionCommitsOneHatAndReplaysWithoutReroll(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	actor := actors[0]
	actor.Character.MapID = 40000
	if err := s.SaveCharacter(ctx, actor, actor.Character); err != nil {
		t.Fatal(err)
	}
	actor.Character.Revision++
	start, err := s.TransitionQuest(ctx, domain.QuestRequest{Session: actor, QuestID: 1035, NPCID: 2004, Action: "start", OperationID: fmt.Sprintf("weighted-hat-start-%d", actor.CharacterID)})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = start.Character
	actor.Character, err = s.AwardCombatKill(ctx, actor, fmt.Sprintf("weighted-hat-kill-%d", actor.CharacterID), 0, 9300018)
	if err != nil {
		t.Fatal(err)
	}
	// Persistence setup only. Real-client acceptance must obtain this input
	// through the mob drop and native pickup rather than this fixture exchange.
	input, err := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: actor, OperationID: fmt.Sprintf("weighted-hat-input-%d", actor.CharacterID), Items: []domain.ScriptItemDelta{{TemplateID: 4031802, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = input.Character
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot) SELECT $1,1322005,n FROM generate_series(1,24) n`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	actor.Character, err = s.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req := domain.QuestRequest{Session: actor, QuestID: 1035, NPCID: 2002, Action: "complete", Selection: -1, OperationID: fmt.Sprintf("weighted-hat-complete-%d", actor.CharacterID)}
	if _, err = s.TransitionQuest(ctx, req); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full equipment bag accepted: %v", err)
	}
	unchanged, err := s.GetCharacter(ctx, actor.CharacterID)
	if err != nil || findQuest(unchanged.Quests, 1035).State != 1 || itemQuantity(unchanged, 4031802) != 1 || unchanged.EXP != actor.Character.EXP {
		t.Fatalf("full bag partially completed reward: %+v %v", unchanged, err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM asset_equipment WHERE character_id=$1 AND slot=24`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	actor.Character, err = s.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req.Session = actor
	got, err := s.TransitionQuest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Quest.State != 2 || itemQuantity(got.Character, 4031802) != 0 {
		t.Fatalf("input/quest not committed: %+v", got)
	}
	var hatID int64
	count := 0
	for _, e := range got.Character.Equipment {
		switch e.ItemID {
		case 1002053, 1002008, 1002014, 1002068, 1002069:
			count++
			hatID = e.ID
		}
	}
	if count != 1 {
		t.Fatalf("weighted reward granted %d hats", count)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replay, err := s.TransitionQuest(ctx, req)
			if err != nil || !replay.Replayed {
				t.Errorf("replay: %v %+v", err, replay)
				return
			}
			n := 0
			for _, e := range replay.Character.Equipment {
				if e.ID == hatID {
					n++
				}
			}
			if n != 1 {
				t.Errorf("receipt rerolled/lost hat %d", hatID)
			}
		}()
	}
	wg.Wait()
	reloaded, err := s.GetCharacter(ctx, actor.CharacterID)
	if err != nil || findQuest(reloaded.Quests, 1035).State != 2 || itemQuantity(reloaded, 4031802) != 0 {
		t.Fatalf("reload %+v %v", reloaded, err)
	}
	count = 0
	for _, e := range reloaded.Equipment {
		if e.ID == hatID {
			count++
		}
	}
	if count != 1 {
		t.Fatal("hat missing after reload")
	}
}
