package content

import (
	"encoding/xml"
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func requirementFrom(t *testing.T, source string) QuestRequirement {
	t.Helper()
	var n wzNode
	if err := xml.Unmarshal([]byte(source), &n); err != nil {
		t.Fatal(err)
	}
	return parseQuestRequirement(&n)
}

func TestQuestInfoDemandReadsTheCustomDataRecord(t *testing.T) {
	now := time.Now()
	c := domain.Character{Level: 30, HP: 1, QuestRecords: map[int]string{2311: "1", 2314: "7", 9000: "clear=1;use=0"}}
	met := func(req QuestRequirement, quest int) error {
		return QuestRequirementMet(c, domain.QuestState{ID: quest}, req, 0, "", now)
	}
	exact := requirementFrom(t, `<imgdir><int name="infoNumber" value="2311"/><imgdir name="infoex"><imgdir name="0"><string name="value" value="1"/></imgdir></imgdir></imgdir>`)
	if len(exact.Unsupported) != 0 || exact.InfoQuest != 2311 || met(exact, 2312) != nil {
		t.Fatalf("exact record: %+v %v", exact, met(exact, 2312))
	}
	c.QuestRecords[2311] = "2"
	if !errors.Is(met(exact, 2312), domain.ErrForbidden) {
		t.Fatal("a different record satisfied the demand")
	}
	// Without infoNumber the quest's own record is read; any entry may match,
	// and cond 1 is a minimum.
	own := requirementFrom(t, `<imgdir><imgdir name="infoex"><imgdir name="0"><string name="value" value="x"/></imgdir><imgdir name="1"><string name="value" value="5"/><int name="cond" value="1"/></imgdir></imgdir></imgdir>`)
	if len(own.Unsupported) != 0 || met(own, 2314) != nil {
		t.Fatalf("minimum record: %+v %v", own, met(own, 2314))
	}
	if !errors.Is(met(own, 2315), domain.ErrForbidden) {
		t.Fatal("a missing record satisfied the demand")
	}
	keyed := requirementFrom(t, `<imgdir><int name="infoNumber" value="9000"/><imgdir name="infoex"><imgdir name="0"><string name="value" value="1"/><int name="cond" value="1"/><string name="exVariable" value="clear"/></imgdir></imgdir></imgdir>`)
	if len(keyed.Unsupported) != 0 || met(keyed, 1) != nil {
		t.Fatalf("keyed record: %+v %v", keyed, met(keyed, 1))
	}
	keyed.InfoEx[0].Variable = "use"
	if !errors.Is(met(keyed, 1), domain.ErrForbidden) {
		t.Fatal("the wrong key satisfied the demand")
	}
	for _, source := range []string{
		`<imgdir><imgdir name="infoex"><imgdir name="0"><string name="value" value="2"/><int name="cond" value="2"/></imgdir></imgdir></imgdir>`,
		`<imgdir><imgdir name="infoex"><imgdir name="0"><string name="value" value="ing"/><int name="cond" value="1"/></imgdir></imgdir></imgdir>`,
		`<imgdir><imgdir name="infoex"><imgdir name="0"><string name="value" value="1"/><int name="order" value="1"/></imgdir></imgdir></imgdir>`,
		`<imgdir><imgdir name="infoex"></imgdir></imgdir>`,
	} {
		if r := requirementFrom(t, source); len(r.Unsupported) == 0 {
			t.Fatalf("accepted %s", source)
		}
	}
}

func TestRepeatableQuestWaitsForItsInterval(t *testing.T) {
	now := time.Now()
	c := domain.Character{Level: 30, HP: 1}
	done := domain.QuestState{ID: 3941, State: 2, CompletedAt: now.Add(-30 * time.Minute)}
	once := requirementFrom(t, `<imgdir><int name="interval" value="0"/><string name="start_t" value="201102210000"/></imgdir>`)
	if len(once.Unsupported) != 0 || !errors.Is(QuestRequirementMet(c, done, once, 0, "", now), domain.ErrForbidden) {
		t.Fatalf("a finished one-time quest may restart: %+v", once)
	}
	hourly := requirementFrom(t, `<imgdir><int name="interval" value="60"/></imgdir>`)
	if !errors.Is(QuestRequirementMet(c, done, hourly, 0, "", now), domain.ErrForbidden) {
		t.Fatal("interval ignored")
	}
	done.CompletedAt = now.Add(-61 * time.Minute)
	if err := QuestRequirementMet(c, done, hourly, 0, "", now); err != nil {
		t.Fatal(err)
	}
}

func TestQuestSubJobFlagsAdmitOnlyTheirSubcategory(t *testing.T) {
	now := time.Now()
	// 2573 (Cannoneer ship), 2350 (Dual Blade), 1048 (ordinary), 52400 (Jett).
	for _, tc := range []struct {
		flags, subJob int
		ok            bool
	}{{4, domain.SubJobCannoneer, true}, {4, domain.SubJobNone, false}, {4, domain.SubJobDualBlade, false},
		{2, domain.SubJobDualBlade, true}, {2, domain.SubJobNone, false}, {1, domain.SubJobNone, true},
		{1, domain.SubJobCannoneer, false}, {1024, 10, true}, {1024, domain.SubJobNone, false}} {
		req := requirementFrom(t, fmt.Sprintf(`<imgdir><int name="subJobFlags" value="%d"/><imgdir name="job"><int name="0" value="0"/></imgdir></imgdir>`, tc.flags))
		if len(req.Unsupported) != 0 || req.SubJobFlags != tc.flags {
			t.Fatalf("flags %d: %+v", tc.flags, req)
		}
		err := QuestRequirementMet(domain.Character{Level: 1, HP: 1, SubJob: tc.subJob}, domain.QuestState{ID: 2573}, req, 0, "", now)
		if (err == nil) != tc.ok {
			t.Fatalf("flags %d sub job %d: %v", tc.flags, tc.subJob, err)
		}
	}
	if r := requirementFrom(t, `<imgdir><int name="subJobFlags" value="0"/></imgdir>`); len(r.Unsupported) == 0 {
		t.Fatal("an empty mask was accepted")
	}
}
