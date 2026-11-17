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
		{name: "swift phantom with party haste", equipment: []domain.Equipment{shoes}, passive: phantom, buff: haste, speed: 160, jump: 123},
		{name: "combat step with movement equipment", equipment: []domain.Equipment{shoes, cape}, passive: aran, speed: 160, jump: 123},
		{name: "haste learner before casting", equipment: []domain.Equipment{shoes, cape}, passive: learnedHaste, speed: 150, jump: 123},
		{name: "haste recipient", equipment: []domain.Equipment{shoes, cape}, buff: haste, speed: 140, jump: 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got combat.PlayerStats
			foldMovement(&got, domain.Character{Equipment: tc.equipment}, Bonus{}, tc.passive, tc.buff)
			if got.Speed != tc.speed || got.Jump != tc.jump {
				t.Fatalf("movement = %d / %d, want %d / %d", got.Speed, got.Jump, tc.speed, tc.jump)
			}
		})
	}
	if got := phantom.Add(learnedHaste).SpeedLimitBonus; got != 20 {
		t.Fatalf("learned speed caps stacked to %d", got)
	}
}
