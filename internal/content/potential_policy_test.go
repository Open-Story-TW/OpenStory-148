package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
)

func potentialPolicyFixture(t *testing.T) *PotentialCatalog {
	t.Helper()
	var xml strings.Builder
	xml.WriteString(`<imgdir name="ItemOption.img">`)
	for _, row := range []struct{ id, kind, req, weight int }{
		{1, 0, 0, 100}, {10001, 0, 0, 20}, {10002, 0, 0, 1}, {10221, 10, 10, 5},
		{20001, 0, 0, 3}, {30001, 0, 0, 2}, {40001, 0, 0, 1}, {40650, 40, 0, 5},
		{40656, 40, 0, 5}, {50001, 0, 0, 1}, {60003, 0, 0, 1},
	} {
		fmt.Fprintf(&xml, `<imgdir name="%06d"><imgdir name="info"><int name="optionType" value="%d"/><int name="reqLevel" value="%d"/><int name="weight" value="%d"/></imgdir><imgdir name="level">`, row.id, row.kind, row.req, row.weight)
		for band := 1; band <= 20; band++ {
			fmt.Fprintf(&xml, `<imgdir name="%d"><int name="incSTR" value="%d"/><string name="future" value="band%d"/></imgdir>`, band, band+1, band)
		}
		xml.WriteString(`</imgdir></imgdir>`)
	}
	xml.WriteString(`</imgdir>`)
	c, err := parsePotentialCatalog([]byte(xml.String()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func potentialTarget(p domain.EquipmentPotential) PotentialTarget {
	return PotentialTarget{ItemID: 1302000, RequiredLevel: 10, UpgradeSlots: 7, Potential: p}
}

func potentialWords(state int, values ...int) domain.EquipmentPotential {
	p := domain.EquipmentPotential{Present: true, RawState: uint8(state), Flags: 0x8085}
	for i, v := range values {
		p.Options[i] = uint16(v)
	}
	return p
}

type potentialTestDie struct {
	bound, value int
}

func potentialDice(t *testing.T, dice ...potentialTestDie) (PotentialRandN, func()) {
	t.Helper()
	i := 0
	return func(bound int) (int, error) {
			if i >= len(dice) {
				return 0, fmt.Errorf("unexpected RNG call %d with bound %d", i, bound)
			}
			d := dice[i]
			i++
			if d.bound != bound {
				return 0, fmt.Errorf("draw %d bound=%d want %d", i, bound, d.bound)
			}
			return d.value, nil
		}, func() {
			if i != len(dice) {
				t.Fatalf("used %d of %d draws", i, len(dice))
			}
		}
}

func potentialOptionDie(t *testing.T, c *PotentialCatalog, id int) potentialTestDie {
	t.Helper()
	for i, found := range c.ids {
		if found == id {
			return potentialTestDie{len(c.ids), i}
		}
	}
	t.Fatal("fixture option absent", id)
	return potentialTestDie{}
}

func TestPotentialSealActualInitialRanksAndNoWZSuccessRoll(t *testing.T) {
	c := potentialPolicyFixture(t)
	for _, tc := range []struct {
		name  string
		dice  []potentialTestDie
		tier  int
		third bool
	}{
		{"rare boundary", []potentialTestDie{{100, 20}, {10, 1}}, 17, false},
		{"epic boundary", []potentialTestDie{{100, 19}, {100, 12}, {10, 0}}, 18, true},
		{"unique boundary", []potentialTestDie{{100, 19}, {100, 11}, {100, 8}, {10, 9}}, 19, false},
		{"legendary boundary", []potentialTestDie{{100, 0}, {100, 0}, {100, 7}, {10, 0}}, 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, scrollID := range []int{2049400, 2049401, 2049402, 2049406, 5534000} {
				randN, done := potentialDice(t, tc.dice...)
				before := potentialWords(0)
				plan, err := SealPotential(c, PotentialSealRequest{Target: potentialTarget(before), ScrollID: scrollID}, DefaultPotentialPolicy(), randN)
				done()
				if err != nil || !plan.ScrollSuccess || plan.After.RawState != 1 || !plan.After.Present || plan.Tier != tc.tier || plan.ToolCount != 1 || plan.MesoFee != 0 {
					t.Fatal("wrong actual potential scroll result", plan, err)
				}
				want := potentialWords(1, -tc.tier)
				if tc.third {
					want.Options[1] = uint16(-tc.tier)
				}
				if plan.After != want || plan.Before != before || plan.WZSHA256 != c.SourceSHA256() || len(plan.Draws) != len(tc.dice) {
					t.Fatal("state/markers/flags/provenance changed", plan)
				}
			}
		})
	}
}

func TestPotentialEpicReachableZeroHiddenAndRareResults(t *testing.T) {
	c := potentialPolicyFixture(t)
	for _, tc := range []struct {
		name    string
		before  domain.EquipmentPotential
		dice    []potentialTestDie
		after   domain.EquipmentPotential
		success bool
	}{
		{"zero grade5 success invalidtier1", potentialWords(0), []potentialTestDie{{1000, 5}, {10, 1}}, potentialWords(1, -1, -1), true},
		{"zero grade6 failure still consumed", potentialWords(0), []potentialTestDie{{1000, 6}, {10, 2}}, potentialWords(0), false},
		{"hidden loses sealed legendary", potentialWords(1, -20), []potentialTestDie{{1000, 6}, {10, 0}}, potentialWords(1, -1, -1), true},
		{"hidden grade5 invalidtier2", potentialWords(1, -20), []potentialTestDie{{1000, 5}, {10, 2}}, potentialWords(1, -2), true},
		{"rare promotion grade19", potentialWords(17, 10001, 10002), []potentialTestDie{{1000, 19}, {10, 2}}, potentialWords(1, -18), true},
		{"rare no promotion grade20", potentialWords(17, 10001, 10002), []potentialTestDie{{1000, 20}, {10, 1}}, potentialWords(1, -17, -17), true},
		{"old three lines no line RNG", potentialWords(17, 10001, 10002, 10001), []potentialTestDie{{1000, 999}}, potentialWords(1, -17, -17), true},
		{"old five rows become markers", potentialWords(17, 10001, 10002, 10001, 10002, 10001), []potentialTestDie{{1000, 0}}, potentialWords(1, -18, -18, -18, -18), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, scroll := range []int{2049700, 2049701} {
				randN, done := potentialDice(t, tc.dice...)
				plan, err := SealPotential(c, PotentialSealRequest{Target: potentialTarget(tc.before), ScrollID: scroll}, DefaultPotentialPolicy(), randN)
				done()
				if err != nil || plan.After != tc.after || plan.ScrollSuccess != tc.success || plan.ToolCount != 1 {
					t.Fatal(plan, err)
				}
			}
		})
	}
}

func TestPotentialSealDenialsDoNotDrawOrReturnSpendablePlan(t *testing.T) {
	c := potentialPolicyFixture(t)
	base := PotentialSealRequest{Target: potentialTarget(potentialWords(0)), ScrollID: 2049400}
	for name, mutate := range map[string]func(*PotentialSealRequest){
		"ordinary hidden":         func(r *PotentialSealRequest) { r.Target.Potential = potentialWords(1, -17) },
		"ordinary revealed":       func(r *PotentialSealRequest) { r.Target.Potential = potentialWords(17, 10001) },
		"epic epic":               func(r *PotentialSealRequest) { r.ScrollID = 2049700; r.Target.Potential = potentialWords(18, 20001) },
		"epic unique":             func(r *PotentialSealRequest) { r.ScrollID = 2049700; r.Target.Potential = potentialWords(19, 30001) },
		"epic legendary":          func(r *PotentialSealRequest) { r.ScrollID = 2049700; r.Target.Potential = potentialWords(20, 40001) },
		"cash":                    func(r *PotentialSealRequest) { r.Target.Cash = true },
		"vegas":                   func(r *PotentialSealRequest) { r.Vegas = 1 },
		"no slot or scroll level": func(r *PotentialSealRequest) { r.Target.UpgradeSlots = 0 },
		"mech":                    func(r *PotentialSealRequest) { r.Target.ItemID = 1902000 },
		"normal scroll":           func(r *PotentialSealRequest) { r.ScrollID = 2040000 },
		"opaque native state":     func(r *PotentialSealRequest) { r.Target.Potential = potentialWords(0x91, 10001) },
		"incoherent native state": func(r *PotentialSealRequest) { r.Target.Potential = potentialWords(17, 40001) },
		"negative level":          func(r *PotentialSealRequest) { r.Target.RequiredLevel = -1 },
		"slots overflow":          func(r *PotentialSealRequest) { r.Target.UpgradeSlots = 256 },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			plan, err := SealPotential(c, r, DefaultPotentialPolicy(), func(int) (int, error) { t.Fatal("denial called RNG"); return 0, nil })
			if err == nil || !reflect.DeepEqual(plan, PotentialPlan{}) {
				t.Fatal("denial supplied partial plan", plan, err)
			}
		})
	}
	for _, kind := range []string{"secondary", "already scrolled", "epic no slot"} {
		r := base
		r.Target.UpgradeSlots = 0
		switch kind {
		case "secondary":
			r.Target.ItemID = 1352000
		case "already scrolled":
			r.Target.ScrollLevel = 1
		case "epic no slot":
			r.ScrollID = 2049700
		}
		if _, err := SealPotential(c, r, DefaultPotentialPolicy(), func(int) (int, error) { return 9, nil }); err != nil {
			t.Fatal(kind, err)
		}
	}
}

func TestPotentialPlansAuditJSONKeepsAbsentAndExplicitZeroDistinct(t *testing.T) {
	c := potentialPolicyFixture(t)
	nonzeroAbsent := potentialWords(1, -20)
	nonzeroAbsent.Present = false
	for _, before := range []domain.EquipmentPotential{{}, {Present: true}, potentialWords(0), potentialWords(17, 10001), potentialWords(1, -20), nonzeroAbsent} {
		scroll := 2049700
		if before.RawState == 0 && !before.Present {
			scroll = 2049400
		}
		randN := func(bound int) (int, error) { return bound - 1, nil }
		plan, err := SealPotential(c, PotentialSealRequest{Target: potentialTarget(before), ScrollID: scroll}, DefaultPotentialPolicy(), randN)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var restored PotentialPlan
		if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(plan, restored) {
			t.Fatal("receipt changed potential presence/flags/markers", string(data), plan, restored, err)
		}
		if restored.Before.Present != before.Present || !restored.After.Present {
			t.Fatal("absent and explicitly observed native zero collapsed", restored)
		}
		again, err := json.Marshal(restored)
		if err != nil || !bytes.Equal(data, again) || sha256.Sum256(data) != sha256.Sum256(again) {
			t.Fatal("audit receipt bytes/hash unstable", string(data), string(again), err)
		}
	}
	before := potentialWords(1, -17)
	die := potentialOptionDie(t, c, 10001)
	randN, done := potentialDice(t, die, die)
	plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: potentialTarget(before), MagnifierID: 2460000}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(plan)
	var restored PotentialPlan
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(plan, restored) {
		t.Fatal("roll transcript/stat data changed in receipt JSON", restored, err)
	}
	again, err := json.Marshal(restored)
	if err != nil || !bytes.Equal(data, again) || sha256.Sum256(data) != sha256.Sum256(again) {
		t.Fatal("rolled option/transcript receipt hash unstable", err)
	}
}

func TestPotentialMagnifyUniformRejectionsBandsAndDetachedPlan(t *testing.T) {
	c := potentialPolicyFixture(t)
	before := potentialWords(1, -17)
	randN, done := potentialDice(t, potentialOptionDie(t, c, 1), potentialOptionDie(t, c, 20001), potentialOptionDie(t, c, 10221), potentialOptionDie(t, c, 10221))
	plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: potentialTarget(before), MagnifierID: 2460000}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || plan.After != potentialWords(17, 10221, 10221) || len(plan.Draws) != 4 || plan.Draws[0].SourceOptionID != 1 || plan.MesoFee != 1000 {
		t.Fatal(plan, err)
	}
	if plan.Options[0].CandidateBand != 2 || plan.Options[0].StatBand != 1 || plan.Options[0].Stats.Values["incSTR"] != 2 {
		t.Fatal("generation index was confused with stat band", plan.Options)
	}
	if plan.Draws[2].Bound != len(c.ids) {
		t.Fatal("weights or accepted-pool size used in place of all source rows", plan.Draws)
	}
	plan.Options[0].Stats.Values["incSTR"] = 999
	plan.Options[0].Stats.Text["future"] = "mutated"
	plan.After.Options[0] = 999
	if level, _ := c.Level(10221, 1); level.Values["incSTR"] != 2 || level.Text["future"] != "band1" {
		t.Fatal("plan aliases catalog", level)
	}
	if before != potentialWords(1, -17) {
		t.Fatal("source value mutated")
	}
}

func TestPotentialMagnifyAllTiersMarkersAndInvalidTierFallback(t *testing.T) {
	c := potentialPolicyFixture(t)
	for _, tc := range []struct{ marker, tier, id int }{{-17, 17, 10001}, {-18, 18, 20001}, {-19, 19, 30001}, {-20, 20, 40001}, {-1, 17, 10001}, {-2, 17, 10001}, {-32768, 17, 10001}} {
		t.Run(fmt.Sprint(tc.marker), func(t *testing.T) {
			for lines := 2; lines <= 5; lines++ {
				before := potentialWords(1, tc.marker)
				for i := 1; i < lines-1; i++ {
					before.Options[i] = uint16(tc.marker)
				}
				dice := make([]potentialTestDie, lines)
				for i := range dice {
					dice[i] = potentialOptionDie(t, c, tc.id)
				}
				randN, done := potentialDice(t, dice...)
				plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: potentialTarget(before), MagnifierID: 2460003}, DefaultPotentialPolicy(), randN)
				done()
				if err != nil || plan.After.RawState != uint8(tc.tier) || plan.Tier != tc.tier || plan.RollLines != lines || len(plan.Options) != lines || plan.After.Flags != before.Flags {
					t.Fatal(plan, err)
				}
				for i, row := range plan.Options {
					if row.Retained != (i < 3) {
						t.Fatal("discarded lines claimed retained", row)
					}
					if i < 3 && plan.After.Options[i] != uint16(tc.id) || i >= 3 && plan.After.Options[i] != 0 {
						t.Fatal("Java's discarded fourth/fifth behavior changed", plan.After)
					}
				}
			}
		})
	}
}

func TestPotentialMagnifySparseMarkersKeepUntouchedJavaWords(t *testing.T) {
	c := potentialPolicyFixture(t)
	before := potentialWords(1, -17, 0, 0, -17)
	die := potentialOptionDie(t, c, 10001)
	randN, done := potentialDice(t, die, die, die)
	plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: potentialTarget(before), MagnifierID: 2460000}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || plan.After != potentialWords(17, 10001, 10001, 10001, -17) {
		t.Fatal("nonvisited marker was silently normalized", plan, err)
	}
	randN, done = potentialDice(t, potentialTestDie{1000, 999})
	sealed, err := SealPotential(c, PotentialSealRequest{Target: potentialTarget(plan.After), ScrollID: 2049700}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || sealed.After != potentialWords(1, -17, -17) {
		t.Fatal("negative marker became unsigned legendary option", sealed, err)
	}
}

func TestPotentialSpecialFallbackIsLocalAndOuterRerollMatchesJava(t *testing.T) {
	c := potentialPolicyFixture(t)
	before, _ := c.Option(40650)
	target := potentialTarget(potentialWords(1, -20))
	target.ItemID = 1032000
	target.RequiredLevel = 70
	randN, done := potentialDice(t,
		potentialOptionDie(t, c, 40650), potentialTestDie{100, 11}, potentialOptionDie(t, c, 30001),
		potentialOptionDie(t, c, 40656), potentialTestDie{100, 10}, potentialOptionDie(t, c, 40650), potentialTestDie{100, 99})
	plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460001}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || plan.After != potentialWords(20, 40656, 10001) || len(plan.Draws) != 7 || plan.Draws[3].Pass != 2 {
		t.Fatal("wrong local fallback or outer reroll", plan, err)
	}
	if plan.Options[1].SourceID != 40650 || plan.Options[1].EffectiveID != 10001 {
		t.Fatal("fallback provenance missing", plan.Options)
	}
	after, _ := c.Option(40650)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("global option row mutated", before, after)
	}
	// Next character sees the original special row, not Java's corrupted opID.
	randN, done = potentialDice(t, potentialOptionDie(t, c, 40650), potentialTestDie{100, 10}, potentialOptionDie(t, c, 40656), potentialTestDie{100, 10})
	next, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460001}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || next.After.Options[0] != 40650 || next.After.Options[1] != 40656 {
		t.Fatal("a previous character poisoned this roll", next, err)
	}
	// Java does not revalidate first-line rank if a later line keeps tier20.
	randN, done = potentialDice(t, potentialOptionDie(t, c, 40650), potentialTestDie{100, 11}, potentialOptionDie(t, c, 40001))
	next, err = MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460001}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || next.After != potentialWords(20, 10001, 40001) || len(next.Draws) != 3 {
		t.Fatal("invented first-line reroll", next, err)
	}
}

func TestPotentialSpecialThresholdIsInclusiveAndDoesNotPoisonEitherRow(t *testing.T) {
	c := potentialPolicyFixture(t)
	target := potentialTarget(potentialWords(1, -20))
	target.ItemID, target.RequiredLevel = 1032000, 70
	for _, source := range []int{40650, 40656} {
		before, _ := c.Option(source)
		for _, tc := range []struct {
			threshold, draw int
			keep            bool
		}{{-1, 0, false}, {0, 0, true}, {0, 1, false}, {10, 10, true}, {10, 11, false}, {99, 99, true}, {100, 99, true}} {
			p := DefaultPotentialPolicy()
			p.SpecialKeepThreshold = tc.threshold
			randN, done := potentialDice(t, potentialOptionDie(t, c, 40001), potentialOptionDie(t, c, source), potentialTestDie{100, tc.draw})
			plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460001}, p, randN)
			done()
			want := 10001
			if tc.keep {
				want = source
			}
			if err != nil || plan.After.Options[1] != uint16(want) || plan.Draws[2].EffectiveOptionID != want {
				t.Fatal(source, tc, plan, err)
			}
		}
		after, _ := c.Option(source)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("special threshold changed shared row", source)
		}
	}
}

func TestPotentialInitialRankUnconditionalProbabilityMass(t *testing.T) {
	c := potentialPolicyFixture(t)
	counts := make(map[int]int)
	run := func(dice []potentialTestDie, mass int) {
		dice = append(dice, potentialTestDie{10, 9})
		randN, done := potentialDice(t, dice...)
		plan, err := SealPotential(c, PotentialSealRequest{Target: potentialTarget(potentialWords(0)), ScrollID: 2049400}, DefaultPotentialPolicy(), randN)
		done()
		if err != nil {
			t.Fatal(err)
		}
		counts[plan.Tier] += mass
	}
	// Enumerate the actual conditional calls. Unused subsequent random digits
	// represent probability mass, not imaginary RNG calls in the transcript.
	for a := 0; a < 100; a++ {
		if a >= 20 {
			run([]potentialTestDie{{100, a}}, 10000)
			continue
		}
		for b := 0; b < 100; b++ {
			if b >= 12 {
				run([]potentialTestDie{{100, a}, {100, b}}, 100)
				continue
			}
			for d := 0; d < 100; d++ {
				run([]potentialTestDie{{100, a}, {100, b}, {100, d}}, 1)
			}
		}
	}
	if !reflect.DeepEqual(counts, map[int]int{17: 800000, 18: 176000, 19: 22080, 20: 1920}) {
		t.Fatal("comment rates replaced actual unconditional rates", counts)
	}
}

func TestPotentialFeesBandsAndJavaMagnifierCaps(t *testing.T) {
	for _, tc := range []struct {
		level int
		fees  [4]int
	}{{0, [4]int{1000, 5000, 10000, 15000}}, {100, [4]int{1000, 5000, 10000, 15000}}, {101, [4]int{5000, 10000, 30000, 50000}}, {120, [4]int{5000, 10000, 30000, 50000}}, {121, [4]int{50000, 100000, 150000, 200000}}, {130, [4]int{50000, 100000, 150000, 200000}}, {131, [4]int{50000, 200000, 420000, 550000}}} {
		for i, want := range tc.fees {
			if fee, err := PotentialMagnifyFee(tc.level, 17+i); err != nil || fee != want {
				t.Fatal(tc, fee, err)
			}
		}
	}
	for _, tc := range []struct{ level, band int }{{0, 1}, {1, 1}, {9, 1}, {10, 1}, {11, 2}, {19, 2}, {20, 2}, {199, 20}, {200, 20}, {201, 21}, {210, 21}, {211, 1}, {65535, 1}} {
		if band, err := PotentialOrdinaryNoOverrideStatBand(tc.level); err != nil || band != tc.band {
			t.Fatal(tc, band, err)
		}
	}
	c := potentialPolicyFixture(t)
	for _, limit := range []struct{ tool, max int }{{2460000, 39}, {2460001, 79}, {2460002, 129}, {2460003, 200}} {
		for _, level := range []int{limit.max - 9, limit.max - 8, limit.max, limit.max + 1} {
			target := potentialTarget(potentialWords(1, -17))
			target.RequiredLevel = level
			calls := 0
			_, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: limit.tool}, DefaultPotentialPolicy(), func(int) (int, error) { calls++; return 1, nil })
			wantOK := level <= limit.max
			if limit.tool == 2460003 && level == 201 {
				wantOK = false
			} // no source stat band21
			if (err == nil) != wantOK || !wantOK && calls != 0 {
				t.Fatal(limit, level, err, calls)
			}
		}
	}
}

func TestPotentialNativeBandFlagsOverridesAndCapPrecedence(t *testing.T) {
	for _, tc := range []struct {
		level, override int
		flags           uint16
		want            int
	}{
		{0, 0, 0, 1}, {0, 0, 0x400, 12}, {0, 0, 0x800, 13}, {0, 0, 0xC00, 13},
		{120, 0, 0, 12}, {120, 0, 0x800, 13}, {200, 0, 0x800, 20},
		{0, 1, 0x800, 1}, {0, 2, 0x400, 2}, {120, 3, 0xC00, 3},
		{201, 0, 0x800, 21}, {210, 0, 0x800, 21}, {211, 0, 0x800, 1},
		{211, 20, 0x800, 20}, {211, 21, 0x800, 21}, {211, 22, 0x800, 1},
		{0, -1, 0x800, 13}, {0, -1 << 31, 0x800, 13}, {0, 1<<31 - 1, 0x800, 1},
	} {
		target := PotentialTarget{RequiredLevel: tc.level, ItemFlags: tc.flags}
		target.OptionLevelOverrides[2] = tc.override
		got, err := PotentialStatBand(target, 2)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	// Each line has its own override. Potential.Flags is an unrelated word.
	target := PotentialTarget{Potential: domain.EquipmentPotential{Flags: 0x800}, OptionLevelOverrides: [5]int{1, 2, 0, -1, 3}}
	for line, want := range []int{1, 2, 1, 1, 3} {
		if got, err := PotentialStatBand(target, line); err != nil || got != want {
			t.Fatal(line, got, err)
		}
	}
	for _, line := range []int{-1, 5} {
		if _, err := PotentialStatBand(target, line); !errors.Is(err, ErrPotentialInput) {
			t.Fatal("invalid option index accepted", line, err)
		}
	}
	for _, bad := range []int{-1<<31 - 1, 1 << 31} {
		target.OptionLevelOverrides[0] = bad
		if _, err := PotentialStatBand(target, 0); !errors.Is(err, ErrPotentialInput) {
			t.Fatal("override is not a native I32", bad, err)
		}
	}
}

func TestPotentialMagnifyPerLineNativeBandsDoNotChangeCandidateDraws(t *testing.T) {
	c := potentialPolicyFixture(t)
	target := potentialTarget(potentialWords(1, -17, -17, -17, -17))
	target.RequiredLevel = 120
	target.ItemFlags = 0xC00
	target.OptionLevelOverrides = [5]int{1, 2, 3, 4, 5}
	plain := target
	plain.ItemFlags, plain.OptionLevelOverrides = 0, [5]int{}
	for line := 0; line < 5; line++ {
		baseIDs, _ := EligiblePotentialOptions(c, plain, 17, line, DefaultPotentialPolicy())
		overrideIDs, _ := EligiblePotentialOptions(c, target, 17, line, DefaultPotentialPolicy())
		if !reflect.DeepEqual(baseIDs, overrideIDs) {
			t.Fatal("native stat metadata changed Java candidate membership", line)
		}
	}
	die := potentialOptionDie(t, c, 10001)
	randN, done := potentialDice(t, die, die, die, die, die)
	plan, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460002}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || len(plan.Options) != 5 || len(plan.Draws) != 5 || plan.After.Flags != target.Potential.Flags {
		t.Fatal(plan, err)
	}
	for i, row := range plan.Options {
		if row.CandidateBand != 13 || row.StatBand != i+1 || (row.Retained && row.Stats.Values["incSTR"] != i+2) ||
			(!row.Retained && (row.Stats.Values != nil || row.Stats.Text != nil)) {
			t.Fatal("per-option native level override lost", row)
		}
	}
	data, _ := json.Marshal(plan)
	var restored PotentialPlan
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(plan, restored) {
		t.Fatal("authority facts absent from audit/replay plan", restored, err)
	}
	target.OptionLevelOverrides[4] = 21
	randN, done = potentialDice(t, die, die, die, die, die)
	discarded, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460002}, DefaultPotentialPolicy(), randN)
	done()
	if err != nil || len(discarded.Draws) != 5 || discarded.Options[4].StatBand != 21 || discarded.Options[4].Retained ||
		discarded.Options[4].Stats.Values != nil || discarded.Options[4].Stats.Text != nil || discarded.After.Options[4] != 0 {
		t.Fatal("unused fifth band must not reject or invent applied stats", discarded, err)
	}
	target.OptionLevelOverrides[0] = 21
	zero, err := MagnifyPotential(c, PotentialMagnifyRequest{Target: target, MagnifierID: 2460002}, DefaultPotentialPolicy(), func(int) (int, error) { t.Fatal("missing native band21 consumed RNG"); return 0, nil })
	if !errors.Is(err, ErrPotentialCandidates) || !reflect.DeepEqual(zero, PotentialPlan{}) {
		t.Fatal("invented an absent source band", zero, err)
	}
}

func TestPotentialNoPoolBadRNGAndCapsReturnZeroPlan(t *testing.T) {
	c := potentialPolicyFixture(t)
	req := PotentialMagnifyRequest{Target: potentialTarget(potentialWords(1, -17)), MagnifierID: 2460000}
	for name, rng := range map[string]PotentialRandN{"negative": func(int) (int, error) { return -1, nil }, "upper exclusive": func(n int) (int, error) { return n, nil }, "error": func(int) (int, error) { return 0, errors.New("entropy unavailable") }} {
		t.Run(name, func(t *testing.T) {
			plan, err := MagnifyPotential(c, req, DefaultPotentialPolicy(), rng)
			if !errors.Is(err, ErrPotentialRNG) || !reflect.DeepEqual(plan, PotentialPlan{}) {
				t.Fatal(plan, err)
			}
		})
	}
	p := DefaultPotentialPolicy()
	p.MaxDraws = 3
	calls := 0
	plan, err := MagnifyPotential(c, req, p, func(int) (int, error) { calls++; return 0, nil }) // option1 rejected forever
	if !errors.Is(err, ErrPotentialDrawLimit) || calls != 3 || !reflect.DeepEqual(plan, PotentialPlan{}) {
		t.Fatal(plan, err, calls)
	}
	for _, bad := range []PotentialPolicy{{}, {GradeBound: 1000, MaxDraws: 65537}, {GradeBound: 1000, MaxDraws: 1, SpecialKeepThreshold: 101}} {
		if _, err := SealPotential(c, PotentialSealRequest{Target: req.Target, ScrollID: 2049700}, bad, func(int) (int, error) { t.Fatal("bad configuration used RNG"); return 0, nil }); !errors.Is(err, ErrPotentialInput) {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		level int
		clamp bool
	}{{200, false}, {201, true}} {
		x := req
		x.Target.RequiredLevel = tc.level
		x.MagnifierID = 2460003
		p = DefaultPotentialPolicy()
		p.MysteryLevelClamp = tc.clamp
		plan, err = MagnifyPotential(c, x, p, func(int) (int, error) { t.Fatal("invalid band consumed RNG"); return 0, nil })
		if !errors.Is(err, ErrPotentialCandidates) || !reflect.DeepEqual(plan, PotentialPlan{}) {
			t.Fatal(tc, plan, err)
		}
	}
	copyCatalog := *c
	copyCatalog.options = map[int]PotentialOption{}
	copyCatalog.ids = []int{60003}
	copyCatalog.options[60003] = c.options[60003]
	if _, err := MagnifyPotential(&copyCatalog, req, DefaultPotentialPolicy(), func(int) (int, error) { t.Fatal("empty accepted pool used RNG"); return 0, nil }); !errors.Is(err, ErrPotentialCandidates) {
		t.Fatal(err)
	}
	for _, tc := range []struct{ level, tier int }{{-1, 17}, {65536, 17}, {0, 16}, {0, 21}} {
		if _, err := PotentialMagnifyFee(tc.level, tc.tier); !errors.Is(err, ErrPotentialInput) {
			t.Fatal(tc, err)
		}
	}
	for _, level := range []int{-1, 65536} {
		if _, err := PotentialOrdinaryNoOverrideStatBand(level); !errors.Is(err, ErrPotentialInput) {
			t.Fatal(level, err)
		}
	}
	for _, mutate := range []func(*PotentialMagnifyRequest){
		func(r *PotentialMagnifyRequest) { r.MagnifierID = 2460004 },
		func(r *PotentialMagnifyRequest) { r.Target.Potential = potentialWords(0) },
		func(r *PotentialMagnifyRequest) { r.Target.Potential = potentialWords(17, 10001) },
	} {
		x := req
		mutate(&x)
		plan, err := MagnifyPotential(c, x, DefaultPotentialPolicy(), func(int) (int, error) { t.Fatal("ineligible reveal used RNG"); return 0, nil })
		if !errors.Is(err, ErrPotentialIneligible) || !reflect.DeepEqual(plan, PotentialPlan{}) {
			t.Fatal(plan, err)
		}
	}
}

func TestPotentialOptionMembershipBoundariesAndTypes(t *testing.T) {
	for tier := 17; tier <= 20; tier++ {
		low := (tier - 16) * 10000
		for _, id := range []int{9999, 10000, low - 10000, low - 1, low, low + 9999, low + 10000, 50000, 59999, 60000, 65535} {
			for line := 0; line < 5; line++ {
				want := id >= 10000 && id < 60000 && id <= low+9999 && (line == 0 && id >= low || line > 0 && id >= low-10000)
				if potentialIDFits(id, tier, line) != want {
					t.Fatal(id, tier, line)
				}
			}
		}
	}
	for _, tc := range []struct {
		kind, item int
		want       bool
	}{{10, 1302000, true}, {10, 1599999, true}, {10, 1602000, true}, {10, 1099015, true}, {10, 1099016, false}, {11, 1302000, false}, {11, 1000000, true}, {20, 1000000, true}, {20, 1032000, false}, {40, 1032000, true}, {40, 1113231, true}, {40, 1113216, true}, {40, 1113217, false}, {51, 1000000, true}, {51, 1010000, false}, {52, 1040000, true}, {52, 1050000, true}, {52, 1060000, false}, {53, 1060000, true}, {53, 1050000, true}, {54, 1080000, true}, {55, 1070000, true}, {999, 1302000, true}} {
		if got := PotentialOptionTypeFits(tc.kind, tc.item); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestPotentialAuditedOriginal206RowsAnd32EligibleCases(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_WZ_ROOT for the full original WZ policy audit")
	}
	c, err := LoadPotentialCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.IDs()) != 206 {
		t.Fatal("original catalog changed; redo policy candidate audit", len(c.IDs()))
	}
	data, err := os.ReadFile("testdata/potential-policy-mapping-20261106.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Item  int    `json:"item_id"`
			Level int    `json:"item_req_level"`
			Tier  int    `json:"tier"`
			Line  string `json:"line_class"`
			IDs   []int  `json:"ids"`
		} `json:"fresh_java_candidate_examples"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 32 {
		t.Fatal("missing reviewed candidates", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		line := 0
		if tc.Line == "later" {
			line = 1
		}
		target := PotentialTarget{ItemID: tc.Item, RequiredLevel: tc.Level}
		got, err := EligiblePotentialOptions(c, target, tc.Tier, line, DefaultPotentialPolicy())
		if err != nil || !reflect.DeepEqual(got, tc.IDs) {
			t.Fatalf("item%d level%d tier%d line%d\ngot%v\nwant%v err%v", tc.Item, tc.Level, tc.Tier, line, got, tc.IDs, err)
		}
	}
	t.Log("32 independent reviewed candidate cases, original206x20 catalog", c.SourceSHA256())
}

func TestPotentialAuditedOriginalGloveOptionBandCounterexamples(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_WZ_ROOT for the original WZ info/option override audit")
	}
	for i, id := range []int{1082392, 1082393, 1082394} {
		node, err := readWZ(filepath.Join(root, "Character.wz", "Glove", fmt.Sprintf("%08d.img.xml", id)))
		if err != nil {
			t.Fatal(err)
		}
		info := node.child("info")
		if info == nil || info.intValue("reqLevel", -1) != 120 || info.child("option") == nil || len(info.child("option").Children) != 3 {
			t.Fatal("primary WZ counterexample changed", id)
		}
		target := PotentialTarget{ItemID: id, RequiredLevel: 120}
		for line, row := range info.child("option").Children {
			if row.intValue("option", -1) != 60001+line || row.intValue("level", -1) != i+1 {
				t.Fatal("original option row changed", id, line)
			}
			target.OptionLevelOverrides[line] = row.intValue("level", 0)
		}
		for line := 0; line < 5; line++ {
			want := 12
			if line < 3 {
				want = i + 1
			}
			if got, err := PotentialStatBand(target, line); err != nil || got != want {
				t.Fatal("real source contradicts req-level-only formula", id, line, got, want, err)
			}
		}
	}
}
