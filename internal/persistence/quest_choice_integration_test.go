package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

// The isolated WZ fixture tests persistence semantics only. It is never loaded
// by live QA clients and is not evidence of native quest-choice acceptance.
func TestQuestPlayerChoiceAtomicReplayAndFilteredIndex(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	actor := sessions[0]
	root := t.TempDir()
	write := func(relative, data string) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Quest.wz/Check.img.xml", `<imgdir name="Check.img"><imgdir name="60001"><imgdir name="0"><int name="npc" value="2101"/></imgdir><imgdir name="1"><int name="npc" value="2101"/></imgdir></imgdir></imgdir>`)
	write("Quest.wz/QuestInfo.img.xml", `<imgdir name="QuestInfo.img"><imgdir name="60001"><string name="name" value="isolated reward choice"/></imgdir></imgdir>`)
	write("Quest.wz/Act.img.xml", `<imgdir name="Act.img"><imgdir name="60001"><imgdir name="0"/><imgdir name="1"><int name="exp" value="5"/><imgdir name="item">
<imgdir name="0"><int name="id" value="2000001"/><int name="count" value="9"/><int name="prop" value="-1"/><int name="gender" value="1"/></imgdir>
<imgdir name="1"><int name="id" value="2000001"/><int name="count" value="1"/><int name="prop" value="-1"/><int name="gender" value="0"/></imgdir>
<imgdir name="2"><int name="id" value="2000002"/><int name="count" value="2"/><int name="prop" value="-1"/><int name="gender" value="0"/></imgdir>
<imgdir name="3"><int name="id" value="2000000"/><int name="count" value="-1"/></imgdir>
<imgdir name="4"><int name="id" value="2000001"/><int name="count" value="1"/><int name="prop" value="100"/></imgdir>
</imgdir></imgdir></imgdir></imgdir>`)
	write("Item.wz/Consume/0200.img.xml", `<imgdir name="0200.img"><imgdir name="02000000"><imgdir name="info"><int name="slotMax" value="100"/></imgdir></imgdir><imgdir name="02000001"><imgdir name="info"><int name="slotMax" value="100"/></imgdir></imgdir><imgdir name="02000002"><imgdir name="info"><int name="slotMax" value="100"/></imgdir></imgdir></imgdir>`)
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	start, err := s.TransitionQuest(ctx, domain.QuestRequest{Session: actor, QuestID: 60001, NPCID: 2101, Action: "start", OperationID: fmt.Sprintf("choice-start-operation-%d", actor.CharacterID)})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = start.Character
	request := domain.QuestRequest{Session: actor, QuestID: 60001, NPCID: 2101, Action: "complete", Selection: 1, OperationID: fmt.Sprintf("choice-complete-operation-%d", actor.CharacterID)}
	redBefore := itemQuantity(actor.Character, 2000000)
	for _, choice := range []int{-1, 2, 65535} {
		invalid := request
		invalid.Selection = choice
		if _, err = s.TransitionQuest(ctx, invalid); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("choice %d: %v", choice, err)
		}
	}
	// Fill USE slots so both the selected reward and random reward must roll
	// back the guaranteed consumption, EXP, and completed quest together.
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) SELECT $1,2,n,2000000,1 FROM generate_series(2,24)n`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	actor.Character, err = s.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	request.Session = actor
	if _, err = s.TransitionQuest(ctx, request); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full bag: %v", err)
	}
	unchanged, err := s.GetCharacter(ctx, actor.CharacterID)
	if err != nil || unchanged.EXP != actor.Character.EXP || findQuest(unchanged.Quests, 60001).State != 1 || itemQuantity(unchanged, 2000000) != redBefore+23 {
		t.Fatalf("partial choice commit: %+v %v", unchanged, err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM asset_items WHERE character_id=$1 AND inv_type=2 AND slot>1`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	actor.Character, err = s.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	request.Session = actor
	result, err := s.TransitionQuest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RewardSelection == nil || *result.RewardSelection != 1 || result.Quest.State != 2 || result.Character.EXP != actor.Character.EXP+5 || itemQuantity(result.Character, 2000002) != 2 || itemQuantity(result.Character, 2000001) != 1 || itemQuantity(result.Character, 2000000) != redBefore-1 {
		t.Fatalf("wrong choice: %+v", result)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replay, err := s.TransitionQuest(ctx, request)
			if err != nil || !replay.Replayed || replay.RewardSelection == nil || *replay.RewardSelection != 1 || itemQuantity(replay.Character, 2000002) != 2 {
				t.Errorf("replay: %+v %v", replay, err)
			}
		}()
	}
	wg.Wait()
	changed := request
	changed.Selection = 0
	if _, err = s.TransitionQuest(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("retry changed selection: %v", err)
	}
	reloaded, err := s.GetCharacter(ctx, actor.CharacterID)
	if err != nil || itemQuantity(reloaded, 2000002) != 2 || findQuest(reloaded.Quests, 60001).State != 2 {
		t.Fatalf("choice reload: %+v %v", reloaded, err)
	}
	other, err := s.GetCharacter(ctx, sessions[1].CharacterID)
	if err != nil || findQuest(other.Quests, 60001).State != 0 || itemQuantity(other, 2000002) != 0 {
		t.Fatalf("other character changed: %+v %v", other, err)
	}
}
