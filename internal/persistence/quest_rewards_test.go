package persistence

import (
	"errors"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func rewardInt(n int) *int { return &n }

func TestQuestRewardsUseOneEligibleWeightedPoolAndGuaranteedItems(t *testing.T) {
	items := []content.QuestItem{
		{ID: 1002053, Count: 1, Weight: rewardInt(100), Job: 1},
		{ID: 1002008, Count: 1, Weight: rewardInt(100), Job: 1},
		{ID: 1002014, Count: 1, Weight: rewardInt(9000), Job: 2},
		{ID: 1002068, Count: 1, Weight: rewardInt(9000), Gender: rewardInt(1)},
		{ID: 1002069, Count: 1, Weight: rewardInt(0)},
		{ID: 4031802, Count: -1},
	}
	for _, tc := range []struct {
		draw int64
		want int
	}{{0, 1002053}, {99, 1002053}, {100, 1002008}, {199, 1002008}} {
		got, err := chooseQuestItems(items, domain.Character{Job: 0, Gender: 0}, -1, func(total int64) (int64, error) {
			if total != 200 {
				t.Fatalf("ineligible entries in pool %d", total)
			}
			return tc.draw, nil
		})
		if err != nil || len(got) != 2 || got[0].ID != tc.want || got[1].ID != 4031802 {
			t.Fatalf("draw %d: %+v %v", tc.draw, got, err)
		}
	}
	boom := errors.New("entropy unavailable")
	if _, err := chooseQuestItems(items, domain.Character{}, -1, func(int64) (int64, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatal("entropy failure ignored")
	}
}

func TestQuestRewardJobAndGenderFiltersMatchJavaFamilies(t *testing.T) {
	for _, tc := range []struct {
		item        content.QuestItem
		job, gender int
		want        bool
	}{
		{content.QuestItem{Job: 1}, 0, 0, true}, {content.QuestItem{Job: 1}, 100, 0, false},
		{content.QuestItem{Job: 2}, 100, 0, true}, {content.QuestItem{Job: 2}, 110, 0, true},
		{content.QuestItem{Job: -2145384446}, 0, 0, true},
		{content.QuestItem{Job: 0x40000000}, 3200, 0, true},
		{content.QuestItem{Job: 0x20000}, 2200, 0, true},
		{content.QuestItem{Job: 2, JobEx: 1}, 1200, 0, true},
		{content.QuestItem{Job: 2, JobEx: 1}, 1100, 0, false},
		{content.QuestItem{Gender: rewardInt(0)}, 0, 1, false},
		{content.QuestItem{Gender: rewardInt(2)}, 0, 1, true},
	} {
		if got := eligibleQuestItem(tc.item, domain.Character{Job: tc.job, Gender: tc.gender}); got != tc.want {
			t.Fatalf("item %+v job%d gender%d = %v", tc.item, tc.job, tc.gender, got)
		}
	}
}

func TestQuestPlayerChoiceUsesEligibleEntryIndexAndKeepsRandomPoolSeparate(t *testing.T) {
	items := []content.QuestItem{
		{ID: 1002053, Count: 1, Weight: rewardInt(-1), Gender: rewardInt(1)},
		{ID: 1002008, Count: 1, Weight: rewardInt(-1), Job: 1},
		{ID: 1002014, Count: 2, Weight: rewardInt(-1), Job: 1},
		{ID: 2000001, Count: 3, Weight: rewardInt(10)},
		{ID: 2000000, Count: -1},
	}
	for _, selection := range []int{-1, 2, 65535} {
		_, err := chooseQuestItems(items, domain.Character{}, selection, func(int64) (int64, error) {
			t.Fatal("invalid player choice reached random draw")
			return 0, nil
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid choice %d: %v", selection, err)
		}
	}
	for selection, want := range []int{1002008, 1002014} {
		got, err := chooseQuestItems(items, domain.Character{}, selection, func(total int64) (int64, error) {
			if total != 10 {
				t.Fatalf("choice entries changed random pool: %d", total)
			}
			return 0, nil
		})
		if err != nil || len(got) != 3 || got[0].ID != want || got[1].ID != 2000001 || got[2].Count != -1 {
			t.Fatalf("choice %d: %+v %v", selection, got, err)
		}
	}
	// Two choice entries may name the same item; exactly one entry is selected.
	duplicate := []content.QuestItem{{ID: 2000000, Count: 1, Weight: rewardInt(-1)}, {ID: 2000000, Count: 3, Weight: rewardInt(-1)}}
	got, err := chooseQuestItems(duplicate, domain.Character{}, 1, func(int64) (int64, error) {
		t.Fatal("choice-only reward must not draw entropy")
		return 0, nil
	})
	if err != nil || len(got) != 1 || got[0].Count != 3 {
		t.Fatalf("duplicate item choice granted more than selected entry: %+v %v", got, err)
	}
}
