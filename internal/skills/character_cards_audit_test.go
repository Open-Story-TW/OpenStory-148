package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestV148CardStatsFromExport(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("requires the audit export with Etc.wz/CharacterCard.img")
	}
	if _, err := os.Stat(filepath.Join(root, "Etc.wz", "CharacterCard.img.xml")); err != nil {
		t.Skip("requires the audit export with Etc.wz/CharacterCard.img")
	}
	c := domain.Character{Cards: domain.CharacterCards{
		{CharacterID: 1, Job: 132, Level: 200},
		{CharacterID: 2, Job: 322, Level: 120},
		{CharacterID: 3, Job: 572, Level: 200},
		{CharacterID: 4, Job: 1411, Level: 120},
	}}
	want := Bonus{MaxHPPercent: 5, Critical: 3, CriticalDamageMin: 4, AVOIDPercent: 3}
	if got := Bonuses(c, time.Now()); got != want {
		t.Fatalf("card contributions = %+v, want %+v", got, want)
	}
	c.Cards[1] = domain.CharacterCard{}
	want.Critical = 0
	if got := Bonuses(c, time.Now()); got != want {
		t.Fatalf("card removal = %+v, want %+v", got, want)
	}
	hpFlat, _, hpPct, _, _ := vitalsPassives(c, time.Now())
	if hpFlat != 0 || hpPct != 5 {
		t.Fatalf("card vital projection = %d flat and %d%%, want 0 flat and 5%%", hpFlat, hpPct)
	}
	c.Abilities[1] = domain.InnerAbility{ID: 70000008, Level: 3, Grade: 1}
	c.HonorLevel = 29
	hpFlat, _, _, _, _ = vitalsPassives(c, time.Now())
	if hpFlat != 0 {
		t.Fatalf("locked slot changed vitals by %d", hpFlat)
	}
	c.HonorLevel = 30
	hpFlat, _, hpPct, _, _ = vitalsPassives(c, time.Now())
	if hpFlat != 45 || hpPct != 5 {
		t.Fatalf("combined vital projection = %d flat and %d%%, want 45 flat and 5%%", hpFlat, hpPct)
	}
}

func TestV148CardAvoidabilityFromExport(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if _, err := os.Stat(filepath.Join(root, "Etc.wz", "CharacterCard.img.xml")); err != nil {
		t.Skip("requires the audit export with Etc.wz/CharacterCard.img")
	}
	c := domain.Character{Job: 112, Level: 120, STR: 103, DEX: 101, INT: 104, LUK: 102}
	now := time.Now()
	check := func(physical, magic int) {
		t.Helper()
		got, err := PlayerStatsChecked(c, now)
		if err != nil || got.Avoid != physical || got.MagicAvoid != magic {
			t.Fatalf("EVA got %d/%d, want %d/%d (%v)", got.Avoid, got.MagicAvoid, physical, magic, err)
		}
	}
	check(223, 225)
	c.Cards[0] = domain.CharacterCard{CharacterID: 1, Job: 434, Level: 200}
	check(234, 236)
	c.STR, c.DEX, c.INT, c.LUK = 100, 100, 100, 100
	c.Traits.Insight = 181
	check(241, 241)
}
