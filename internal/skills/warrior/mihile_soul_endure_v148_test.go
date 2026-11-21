package warrior

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
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

func TestMihileRighteousIndignationAndSoulRageV148(t *testing.T) {
	if h, ok := skills.Lookup(51110002); !ok || h.Kind != skills.KindPassive {
		t.Fatalf("51110002 handler=%+v ok=%v", h, ok)
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("requires exported original v148 WZ")
	}
	def, err := content.SkillDefinition(root, 51121006)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := skills.Lookup(51121006)
	if !ok || h.Cast == nil {
		t.Fatal("missing Soul Rage handler")
	}
	values, err := def.Values(30)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Values: values, Level: 30})
	if err != nil {
		t.Fatal(err)
	}
	want := []skills.StatValue{stat("ENRAGE", 6001), stat("SHARP_EYES", 20<<8|15)}
	if p.Self == nil || p.Party != nil || p.MPCost != 50 || p.Cooldown != 270*time.Second || p.Self.Duration != 240*time.Second || !reflect.DeepEqual(p.Self.Stats, want) {
		t.Fatalf("soulRage plan=%+v buff=%+v", p, p.Self)
	}
	wire := make([]protocol.BuffStat, 0, len(want))
	for _, s := range want {
		wire = append(wire, protocol.BuffStat{Stat: s.Stat, Value: s.Value})
	}
	if _, err := protocol.TemporaryStatSet(51121006, p.Self.Duration, wire); err != nil {
		t.Fatal(err)
	}
}

func TestMihileRighteousIndignationCeiling(t *testing.T) {
	mihile := domain.Character{
		Job:    5112,
		Skills: []domain.SkillState{{ID: 51110002, Level: 20}},
	}
	base := 100000
	if combat.MaxLineDamage() > 10_000_000_000 {
		base = 9_117_426_557
	}
	// Active Stun, Freeze, or Darkness scales physical ceiling by (100+x)% = 120% at max level 20.
	for _, st := range []combat.MobStat{combat.MobStun, combat.MobFreeze, combat.MobDarkness} {
		dc := combat.DamageContext{
			Attacker: mihile,
			Statuses: combat.MobStatuses{st: {Stat: st, Value: 1}},
		}
		got := righteousIndignationCeiling(dc, base)
		want := int(min(int64(base)*120/100, int64(combat.MaxLineDamage())))
		if got != want {
			t.Fatalf("status %v: got=%d want=%d", st, got, want)
		}
	}
	// No qualifying status, magic attack, or non-Mihile job leaves ceiling unchanged.
	for name, dc := range map[string]combat.DamageContext{
		"clean":      {Attacker: mihile},
		"poison":     {Attacker: mihile, Statuses: combat.MobStatuses{combat.MobPoison: {Stat: combat.MobPoison, Value: 1}}},
		"magic":      {Attacker: mihile, Magic: true, Statuses: combat.MobStatuses{combat.MobStun: {Stat: combat.MobStun, Value: 1}}},
		"non_mihile": {Attacker: domain.Character{Job: 112, Skills: mihile.Skills}, Statuses: combat.MobStatuses{combat.MobStun: {Stat: combat.MobStun, Value: 1}}},
		"unlearned":  {Attacker: domain.Character{Job: 5112}, Statuses: combat.MobStatuses{combat.MobStun: {Stat: combat.MobStun, Value: 1}}},
	} {
		if got := righteousIndignationCeiling(dc, base); got != base {
			t.Fatalf("%s: got=%d want=%d", name, got, base)
		}
	}
}
