package thief

import (
	"os"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestV148FatalVenomUsesItsWZSource(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("v148 WZ export is required")
	}
	for _, row := range []struct {
		id, job, weapon int
		katara          bool
	}{
		{4120011, 412, 1472000, false},
		{4220011, 422, 1332000, false},
		{4340012, 434, 1332000, true},
	} {
		def, err := content.SkillDefinition(root, row.id)
		if err != nil {
			t.Fatal(err)
		}
		v, err := def.Values(10)
		if err != nil || v["dotSuperpos"] != 3 || v["prop"] != 50 || v["dotInterval"] != 1 || v["dotTime"] != 8 {
			t.Fatalf("unexpected v148 source %d: %v %v", row.id, v, err)
		}
		c := learnedChar(row.job, domain.SkillState{ID: row.id, Level: 10})
		if row.katara {
			c = learnedChar(row.job, domain.SkillState{ID: 4220011, Level: 10}, domain.SkillState{ID: row.id, Level: 10})
			c.Equipment = []domain.Equipment{{Slot: -10, ItemID: 1342000}}
		}
		stats := combat.PlayerStats{Job: row.job, WeaponID: row.weapon, LUK: 400, DEX: 50, WATK: 100}
		ctx := &combat.AttackContext{Character: c, Stats: stats, Now: time.Unix(1000, 0)}
		if err := venom(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 {
			t.Fatalf("source %d did not proc: %v %v", row.id, ctx.Effect.MonsterStatus, err)
		}
		got := ctx.Effect.MonsterStatus[0]
		wantDamage := int64(v["dot"]) * combat.MaxBaseDamage(stats) / 100
		if got.Stat != combat.MobBurned || got.SourceSkill != row.id || got.Prop != 50 ||
			got.StackLimit != 3 || got.DOTInterval != time.Second || got.Duration != 8*time.Second || int64(got.Value) != wantDamage {
			t.Fatalf("source %d disagreed with v148 WZ: %+v, damage want %d", row.id, got, wantDamage)
		}
		if row.katara {
			ctx.Character.Equipment = nil
			ctx.Effect.MonsterStatus = nil
			if err := venom(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 0 {
				t.Fatalf("source %d proc without required katara: %v %v", row.id, ctx.Effect.MonsterStatus, err)
			}
		}
	}
}
