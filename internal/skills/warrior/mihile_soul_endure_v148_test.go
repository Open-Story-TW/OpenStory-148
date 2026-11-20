package warrior

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestMihileSoulEndureOriginalV148Values(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("requires exported original v148 WZ")
	}
	def, err := content.SkillDefinition(root, 51111004)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := skills.Lookup(51111004)
	if !ok || h.Cast == nil {
		t.Fatal("missing Soul Endure handler")
	}
	for _, tc := range []struct{ rank, mp, seconds, defense, resist int }{{1, 20, 66, 5, 3}, {20, 40, 180, 100, 60}} {
		values, err := def.Values(tc.rank)
		if err != nil {
			t.Fatal(err)
		}
		p, err := h.Cast(&skills.CastContext{Skill: def, Values: values, Level: tc.rank})
		if err != nil {
			t.Fatal(err)
		}
		want := []skills.StatValue{stat("ABNORMAL_STATUS_R", tc.resist), stat("ELEMENTAL_STATUS_R", tc.resist), stat("DEFENCE_BOOST_R", tc.defense)}
		if p.Self == nil || p.Party != nil || p.MPCost != tc.mp || p.Self.Duration != time.Duration(tc.seconds)*time.Second || !reflect.DeepEqual(p.Self.Stats, want) {
			t.Fatalf("rank%d plan=%+v buff=%+v", tc.rank, p, p.Self)
		}
		wire := make([]protocol.BuffStat, 0, len(want))
		for _, s := range want {
			wire = append(wire, protocol.BuffStat{Stat: s.Stat, Value: s.Value})
		}
		if _, err := protocol.TemporaryStatSet(51111004, p.Self.Duration, wire); err != nil {
			t.Fatal(err)
		}
	}
}
