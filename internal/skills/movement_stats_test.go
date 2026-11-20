package skills

import (
	"testing"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
)

func TestV148MovementCaps(t *testing.T) {
	phantom := Bonus{Speed: 35, Jump: 20, SpeedLimitBonus: 20}
	aran := Bonus{Speed: 15, SpeedLimitBonus: 20}
	haste := Bonus{Speed: 40, Jump: 20}
	learnedHaste := Bonus{SpeedLimitBonus: 20}
	shoes := domain.Equipment{Slot: -7, Speed: 40, Jump: 30}
	cape := domain.Equipment{Slot: -9, Speed: 10}
	for _, tc := range []struct {
		name          string
		equipment     []domain.Equipment
		passive, buff Bonus
		speed, jump   int
	}{
		{name: "ordinary", equipment: []domain.Equipment{shoes}, speed: 140, jump: 123},
		{name: "swift phantom without shoes", passive: phantom, speed: 135, jump: 120},
		{name: "swift phantom with shoes", equipment: []domain.Equipment{shoes}, passive: phantom, speed: 160, jump: 123},
		{name: "swift phantom with party haste", equipment: []domain.Equipment{shoes}, passive: phantom, buff: haste, speed: 160, jump: 123},
		{name: "combat step with movement equipment", equipment: []domain.Equipment{shoes, cape}, passive: aran, speed: 160, jump: 123},
		{name: "haste learner before casting", equipment: []domain.Equipment{shoes, cape}, passive: learnedHaste, speed: 150, jump: 123},
		{name: "haste recipient", equipment: []domain.Equipment{shoes, cape}, buff: haste, speed: 140, jump: 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got combat.PlayerStats
			foldDefenseMovement(&got, domain.Character{Equipment: tc.equipment}, 0, 0, Bonus{}, tc.passive, tc.buff, false)
			if got.Speed != tc.speed || got.Jump != tc.jump {
				t.Fatalf("movement = %d / %d, want %d / %d", got.Speed, got.Jump, tc.speed, tc.jump)
			}
		})
	}
	if got := phantom.Add(learnedHaste).SpeedLimitBonus; got != 20 {
		t.Fatalf("learned speed caps stacked to %d", got)
	}
}

func TestV148DefensePercentUsesSeparateBases(t *testing.T) {
	// The same v148 client panel at Energize ranks 0 and 30 displays
	// 581 -> 1452 WDEF and 602 -> 1505 MDEF.
	stats := combat.PlayerStats{WDEF: 581, MDEF: 602}
	energize := Bonus{WDEFPercent: 150, MDEFPercent: 150}
	foldDefenseMovement(&stats, domain.Character{}, 0, 0, Bonus{}, energize, Bonus{}, false)
	if stats.WDEF != 1452 || stats.MDEF != 1505 {
		t.Fatalf("defense = %d / %d, want 1452 / 1505", stats.WDEF, stats.MDEF)
	}
}

func TestV148OrdinaryBasicDefenseMatchesOriginalPanel(t *testing.T) {
	armor := []domain.Equipment{{Slot: -7, WDEF: 3}, {Slot: -6, WDEF: 3}, {Slot: -5, WDEF: 3}}
	for _, sample := range []struct {
		name                      string
		str, dex, intl, luk, will int
		percent                   int
		wdef, mdef                int
	}{
		{"baseline", 280, 280, 300, 100, 0, 0, 581, 602},
		{"STR", 300, 280, 300, 100, 0, 0, 611, 602},
		{"DEX", 280, 300, 300, 100, 0, 0, 589, 610},
		{"INT", 280, 280, 320, 100, 0, 0, 581, 632},
		{"LUK", 280, 280, 300, 120, 0, 0, 589, 610},
		{"joint fractions", 281, 281, 301, 101, 0, 0, 583, 604},
		{"before Will level 5", 280, 280, 300, 100, 180, 0, 581, 602},
		{"Will level 5", 280, 280, 300, 100, 181, 0, 586, 607},
		{"Energize scales defense after Will and equipment", 280, 280, 300, 100, 181, 150, 1465, 1517},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c := domain.Character{Equipment: armor, Traits: domain.Traits{Will: sample.will}}
			stats := combat.PlayerStats{STR: sample.str, DEX: sample.dex, INT: sample.intl, LUK: sample.luk}
			bonus := Bonus{WDEFPercent: sample.percent, MDEFPercent: sample.percent}
			foldDefenseMovement(&stats, c, sample.intl, 0, Bonus{}, bonus, Bonus{}, false)
			if stats.WDEF != sample.wdef || stats.MDEF != sample.mdef {
				t.Fatalf("defense = %d / %d, want %d / %d", stats.WDEF, stats.MDEF, sample.wdef, sample.mdef)
			}
		})
	}
}
