package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/domain"
)

func TestDualBladeQuestLineStartsRemotelyAndAdvancesAtomically(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	root := t.TempDir()
	write := func(name, xml string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(xml), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// The v145 root has none of these quests; the v148 supplement answers.
	for _, name := range []string{"Check", "Act", "QuestInfo"} {
		write("Quest.wz/"+name+".img.xml", `<imgdir name="`+name+`.img"></imgdir>`)
	}
	write("Character.wz/Weapon/01342000.img.xml", `<imgdir name="01342000.img"><imgdir name="info"><int name="incPAD" value="17"/><int name="tuc" value="5"/></imgdir></imgdir>`)
	write("Item.wz/Etc/0403.img.xml", `<imgdir name="0403.img"><imgdir name="04032616"><imgdir name="info"><int name="slotMax" value="1"/></imgdir></imgdir></imgdir>`)
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	for _, a := range actors {
		if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET job=400,subcategory=1,level=20,str=4,dex=25,int_stat=4,luk=60,ap=2,sp=1,hp=300,base_max_hp=300,max_hp=300,mp=100,base_max_mp=100,max_mp=100,map_id=103000000 WHERE id=$1`, a.CharacterID); err != nil {
			t.Fatal(err)
		}
	}
	reload := func(i int) {
		t.Helper()
		c, err := store.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		actors[i].Character = c
	}
	reload(0)
	reload(1)
	// 2622 is normalAutoStart: the client starts it away from Lady Syl.
	start := domain.QuestRequest{Session: actors[0], QuestID: 2622, NPCID: 1056000, Action: "start", OperationID: fmt.Sprintf("dual-blade-2622-start-%d", actors[0].CharacterID)}
	result, err := store.TransitionQuest(ctx, start)
	if err != nil || result.Quest.State != 1 {
		t.Fatalf("remote start: %+v %v", result.Quest, err)
	}
	// A scripted start still needs the NPC on the map.
	reload(0)
	scripted := domain.QuestRequest{Session: actors[0], QuestID: 2623, NPCID: 1056000, Action: "start", Script: "quest/2623.lua", OperationID: fmt.Sprintf("dual-blade-2623-start-%d", actors[0].CharacterID)}
	if _, err = store.TransitionQuest(ctx, scripted); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("scripted remote start: %v", err)
	}
	for _, a := range actors {
		if _, err = store.db.ExecContext(ctx, `INSERT INTO game_quests(character_id,quest_id,state,mob_kills) VALUES($1,2622,2,'{}'),($1,2623,1,'{}') ON CONFLICT(character_id,quest_id) DO UPDATE SET state=EXCLUDED.state`, a.CharacterID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,4,1,4032616,1)`, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	reload(0)
	reload(1)
	request := func(i int) domain.JobAdvancementRequest {
		return domain.JobAdvancementRequest{Session: actors[i], OperationID: fmt.Sprintf("dual-blade-430-advance-%d", actors[i].CharacterID), NPCID: 1056000, Script: "quest/2623.lua", TargetJob: 430}
	}
	// Without the 慧眼 the quest's own completion demand refuses.
	if _, err = store.AdvanceFirstJob(ctx, request(1)); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("advanced without the item: %v", err)
	}
	before := actors[0].Character
	got, err := store.AdvanceFirstJob(ctx, request(0))
	if err != nil {
		t.Fatal(err)
	}
	c := got.Character
	if c.Job != 430 || c.SubJob != domain.SubJobDualBlade || c.DEX != 25 || c.LUK != 60 || c.AP != 2 || c.SP != 2 || c.BaseMaxHP < before.BaseMaxHP+350 {
		t.Fatalf("advanced character: %+v", c)
	}
	for inv := 1; inv <= 4; inv++ {
		if c.Capacity(inv) != before.Capacity(inv)+4 {
			t.Fatalf("bag %d: %d -> %d", inv, before.Capacity(inv), c.Capacity(inv))
		}
	}
	katara, eye := 0, 0
	for _, e := range c.Equipment {
		if e.ItemID == 1342000 {
			katara++
		}
	}
	for _, item := range c.Items {
		if item.TemplateID == 4032616 {
			eye += item.Quantity
		}
	}
	if katara != 1 || eye != 0 || findQuest(c.Quests, 2623).State != 2 {
		t.Fatalf("katara=%d eye=%d quest=%+v", katara, eye, findQuest(c.Quests, 2623))
	}
	replay, err := store.AdvanceFirstJob(ctx, request(0))
	if err != nil || !replay.Replayed || replay.Character.Job != 430 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	reloaded, err := store.GetCharacter(ctx, actors[0].CharacterID)
	if err != nil || reloaded.Job != 430 || reloaded.SubJob != domain.SubJobDualBlade || findQuest(reloaded.Quests, 2623).State != 2 {
		t.Fatalf("reload: %+v %v", reloaded, err)
	}
}
