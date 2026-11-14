package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV148DualBladeJobQuestsComeFromTheSupplement(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Quest.wz"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Check.img.xml", "Act.img.xml", "QuestInfo.img.xml"} {
		if err := os.WriteFile(filepath.Join(root, "Quest.wz", name), []byte(`<imgdir name="`+name+`"></imgdir>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"", root} {
		for _, tc := range []struct {
			id, npc, job, level int
			remote              bool
			script              string
		}{
			{2622, 1056000, 400, 20, true, ""},
			{2623, 1056000, 0, 0, false, "quest/2623.lua"},
			{2637, 1056000, 430, 30, true, ""},
			{2638, 1056000, 0, 0, false, "quest/2638.lua"},
			{2641, 1056000, 431, 55, true, ""},
			{2642, 1056000, 0, 0, false, ""},
		} {
			q, err := QuestDefinition(dir, tc.id)
			if err != nil {
				t.Fatalf("%q %d: %v", dir, tc.id, err)
			}
			if n := len(q.Unsupported) + len(q.Start.Unsupported) + len(q.Complete.Unsupported) + len(q.OnStart.Unsupported) + len(q.OnComplete.Unsupported); n != 0 {
				t.Fatalf("%d unsupported: %+v", tc.id, q)
			}
			if q.Start.NPCID != tc.npc || q.Start.MinLevel != tc.level || q.Start.Script != tc.script || q.RemoteStart("start") != tc.remote || q.RemoteStart("complete") {
				t.Fatalf("%d start: %+v", tc.id, q.Start)
			}
			if tc.job != 0 && (len(q.Start.Jobs) != 1 || q.Start.Jobs[0] != tc.job) {
				t.Fatalf("%d jobs %v", tc.id, q.Start.Jobs)
			}
		}
	}
	q, _ := QuestDefinition("", 2622)
	if q.Start.SubJobFlags != 2 || q.Name != "雙刀流" {
		t.Fatalf("2622: %+v", q)
	}
	q, _ = QuestDefinition("", 2642)
	if q.Complete.Script != "quest/2642.lua" || q.Complete.Mobs[6230600] != 99 {
		t.Fatalf("2642 completion: %+v", q.Complete)
	}
	q, _ = QuestDefinition("", 2638)
	if len(q.Complete.Items) != 1 || q.Complete.Items[0].ID != 4031013 || q.Complete.Items[0].Count != 30 {
		t.Fatalf("2638 completion: %+v", q.Complete)
	}
	// The supplement answers only for the quests it lists.
	if _, err := QuestDefinition(root, 1000); err == nil {
		t.Fatal("supplement answered for a quest it does not list")
	}
}
