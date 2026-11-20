package world

import (
	"reflect"
	"testing"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/domain"
)

func TestLostStatsListsEveryStatAWholeBuffResetRemoved(t *testing.T) {
	before := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 35001002, Stat: "MONSTER_RIDING"}, {ID: 35001002, Stat: "ENHANCED_WATK"}, {ID: 35121007, Stat: "MAPLE_WARRIOR"}}}
	after := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 35121007, Stat: "MAPLE_WARRIOR"}}}
	if got := lostStats(before, after); !reflect.DeepEqual(got, []buffstat.Stat{"MONSTER_RIDING", "ENHANCED_WATK"}) {
		t.Fatal(got)
	}
}
