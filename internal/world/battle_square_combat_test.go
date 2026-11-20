package world

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestBattleSquareGuardPointsUseV148DefenseThreshold(t *testing.T) {
	for job, skillID := range map[int]int{100: 1000007, 122: 1000007, 1100: 11000006, 1111: 11000006,
		2100: 21000005, 2112: 21000005, 3100: 31000005, 3112: 31000005} {
		for defense, want := range map[int]int{0: 0, 399: 0, 400: 1, 799: 1, 800: 2, 9999: 24, 20000: 24} {
			got := battleSquareGuardAttack(domain.Character{Job: job}, defense)
			if got.skillID != skillID || got.rating != want {
				t.Errorf("job %d defense %d gave %+v, want skill %d and %d points", job, defense, got, skillID, want)
			}
		}
	}
	for _, job := range []int{0, 200, 400, 5100, 5112} {
		if got := battleSquareGuardAttack(domain.Character{Job: job}, 800); got != (battleSquareGuard{}) {
			t.Fatalf("unrelated job %d gained %+v", job, got)
		}
	}
}

func TestBattleSquareMeleeStatsUseV148BaseDefense(t *testing.T) {
	ch := domain.Character{Job: 100, STR: 4000, DEX: 40, INT: 4, LUK: 4}
	now := time.Unix(100, 0)
	ordinary, err := skills.PlayerStatsChecked(ch, now)
	if err != nil || ordinary.WDEF != 6017 {
		t.Fatalf("unexpected ordinary defense %d: %v", ordinary.WDEF, err)
	}
	arena, err := skills.BattleSquareMeleeStats(ch, now)
	if err != nil || arena.WDEF != 6017 {
		t.Fatalf("unexpected v148 arena defense %d: %v", arena.WDEF, err)
	}
}
