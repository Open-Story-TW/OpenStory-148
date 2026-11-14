package content

import (
	"encoding/xml"
	"testing"
)

func TestStarterQuestDefinitions(t *testing.T) {
	q, e := QuestDefinition("", 1031)
	if e != nil {
		t.Fatal(e)
	}
	if q.Start.NPCID != 2101 || q.Complete.NPCID != 2100 || q.OnComplete.EXP != 5 || len(q.Unsupported)+len(q.Start.Unsupported)+len(q.Complete.Unsupported)+len(q.OnComplete.Unsupported) > 0 {
		t.Fatalf("bad quest1031: %+v", q)
	}
	q, e = QuestDefinition("", 1021)
	if e != nil {
		t.Fatal(e)
	}
	if q.Start.Script != "quest/1021.lua" || q.OnStart.HPDelta != -30 || len(q.OnComplete.Items) != 2 || q.Complete.MinHP != 50 {
		t.Fatalf("bad Roger: %+v", q)
	}
	q, e = QuestDefinition("", 1400)
	if e != nil {
		t.Fatal(e)
	}
	if q.Start.Script != "quest/1400.lua" || q.Complete.Script != "" || q.Start.NPCID != 12100 || q.Start.MinLevel != 5 || len(q.Unsupported)+len(q.Start.Unsupported)+len(q.Complete.Unsupported) > 0 {
		t.Fatalf("bad quest1400: %+v", q)
	}
	q, e = QuestDefinition("", 1000)
	if e != nil {
		t.Fatal(e)
	}
	if q.Start.End.IsZero() || q.Start.End.Year() > 2007 {
		t.Fatal("expired quest window lost")
	}
}
func TestQuestUnknownAndMalformedConditionsFailClosed(t *testing.T) {
	for _, source := range []string{`<imgdir><int name="mystery" value="1"/></imgdir>`, `<imgdir><imgdir name="item"><imgdir name="0"><int name="id" value="2010007"/><int name="count" value="broken"/></imgdir></imgdir></imgdir>`, `<imgdir><imgdir name="mob"><imgdir name="0"><int name="id" value="9300018"/><int name="count" value="1"/><int name="party" value="1"/></imgdir></imgdir></imgdir>`} {
		var n wzNode
		if e := xml.Unmarshal([]byte(source), &n); e != nil {
			t.Fatal(e)
		}
		if r := parseQuestRequirement(&n); len(r.Unsupported) == 0 {
			t.Fatalf("accepted %s", source)
		}
	}
	var n wzNode
	_ = xml.Unmarshal([]byte(`<imgdir><imgdir name="0"><int name="money" value="999"/></imgdir></imgdir>`), &n)
	if a := parseQuestAction(&n); len(a.Unsupported) == 0 {
		t.Fatal("numeric reward directory silently ignored")
	}
}

func TestQuestRewardsRetainConditionsAndRejectUnsupportedFields(t *testing.T) {
	q, err := QuestDefinition("", 1035)
	if err != nil || len(q.OnComplete.Unsupported) > 0 || len(q.OnComplete.Items) != 6 {
		t.Fatalf("1035 weights %+v %v", q, err)
	}
	for _, item := range q.OnComplete.Items[:5] {
		if item.Weight == nil || *item.Weight != 1 || item.Job != 1 {
			t.Fatalf("lost weighted beginner condition: %+v", item)
		}
	}
	if q.OnComplete.Items[5].Weight != nil || q.OnComplete.Items[5].Count != -1 {
		t.Fatal("quest input removal not guaranteed")
	}
	for _, extra := range []string{`<int name="prop" value="-3"/>`, `<int name="period" value="1"/>`, `<int name="gender" value="3"/>`, `<int name="prop" value="100"/><int name="prop" value="1"/>`} {
		var n wzNode
		if err := xml.Unmarshal([]byte(`<imgdir><imgdir name="item"><imgdir name="0"><int name="id" value="1002053"/><int name="count" value="1"/>`+extra+`</imgdir></imgdir></imgdir>`), &n); err != nil {
			t.Fatal(err)
		}
		if a := parseQuestAction(&n); len(a.Unsupported) == 0 {
			t.Fatalf("unsupported reward accepted: %s", extra)
		}
	}
	var choice wzNode
	if err = xml.Unmarshal([]byte(`<imgdir><imgdir name="item"><imgdir name="0"><int name="id" value="1002053"/><int name="count" value="1"/><int name="prop" value="-1"/></imgdir></imgdir></imgdir>`), &choice); err != nil {
		t.Fatal(err)
	}
	a := parseQuestAction(&choice)
	if len(a.Unsupported) != 0 || len(a.Items) != 1 || a.Items[0].Weight == nil || *a.Items[0].Weight != -1 {
		t.Fatalf("player choice was not retained: %+v", a)
	}
}
