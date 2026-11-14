package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// A level 2 beginner with a +500 STR overall hit 菇菇寶貝 for 340-382 on the
// client while the server clipped every hit to 13 (base STR and weapon attack
// only), so the 125 HP monster took about ten hits.
func TestBeginnerNormalAttackCountsEquipmentStats(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Now()
	c := domain.Character{ID: 1, Level: 2, Job: 0, HP: 60, MaxHP: 60, STR: 17, DEX: 5, INT: 4, LUK: 4,
		Equipment: []domain.Equipment{
			{ID: 1, ItemID: 1322005, Slot: -11, WATK: 19},
			{ID: 2, ItemID: 1040002, Slot: -5, STR: 500},
			{ID: 3, ItemID: 1702000, Slot: -111, STR: 900, WATK: 900}, // Cash slot: no stats.
		}}
	mob := content.Mob{ID: 1210102, Level: 10, HP: 125, PDRate: 10}
	got, err := content.NormalAttackMaximum("", c, mob, now)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	want := combat.LineCeiling(combat.DamageContext{Stats: stats, Mob: mob, Effect: combat.Effect{Damage: 100}})
	// 1.2 × (4×517 + 5) × 19 / 100 × 0.9 = 425.4.
	if got != want || got != 426 {
		t.Fatalf("beginner ceiling = %d, generic = %d, want 426", got, want)
	}
	if got < 382 {
		t.Fatalf("observed client hit 382 above ceiling %d", got)
	}
}
