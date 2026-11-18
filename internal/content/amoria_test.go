package content

import "testing"

func TestAmoriaLootUsesOnlyTheOriginalRooms(t *testing.T) {
	for _, tc := range []struct {
		field, mob, item int
	}{
		{670000100, 9400515, AmoriaKey},
		{670000200, 9400516, AmoriaKey},
		{AmoriaMirror, AmoriaFairy, AmoriaHammer},
		{AmoriaCodes, 9400530, AmoriaCode},
		{AmoriaBalrog, AmoriaFinalForm, AmoriaFang},
	} {
		for _, field := range []int{tc.field, 100000000} {
			rows, err := MonsterDrops(tc.mob, field)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range rows {
				if row.TemplateID == tc.item {
					count++
				}
			}
			want := 0
			if field == tc.field {
				want = 1
			}
			if count != want {
				t.Errorf("map %d mob %d item %d had %d drop rows, want %d", field, tc.mob, tc.item, count, want)
			}
		}
	}
}

func TestAmoriaBonusReactorScope(t *testing.T) {
	for template := 6702003; template <= 6702012; template++ {
		if !AmoriaReactor(AmoriaBonus, template) || AmoriaReactor(AmoriaGates, template) || AmoriaReactor(670010750, template) {
			t.Fatalf("unexpected field scope for bonus crate %d", template)
		}
	}
	if AmoriaReactor(AmoriaBonus, 6702002) || AmoriaReactor(AmoriaBonus, 6702013) {
		t.Fatal("bonus field admitted an unrelated reactor")
	}
}
