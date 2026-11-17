package content

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/domain"
)

func TestMonsterRushCombatAuthority(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	mobs := filepath.Join(root, "Mob.wz")
	if err := os.Mkdir(mobs, 0o755); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir><imgdir name="info"><int name="level" value="78"/><int name="maxHP" value="21000"/><int name="maxMP" value="80"/><int name="exp" value="529"/></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(mobs, "3110300.img.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	const town = 120000000
	target := domain.CombatTarget{MobID: MonsterRushMob}
	ordinary, err := CombatTargetExperienceMetadata(root, town, target)
	if err != nil || ordinary.MaximumHP != 21_000 || ordinary.Experience != 529 {
		t.Fatalf("ordinary same-template mob: %+v, %v", ordinary, err)
	}
	target.MonsterRush = true
	template := Mob{ID: MonsterRushMob, Level: 78, HP: 21_000, MP: 80, EXP: 529}
	spawned := MonsterRushStats(template)
	if spawned.ID != template.ID || spawned.Level != template.Level || spawned.HP != MonsterRushHP || spawned.MP != 0 || spawned.EXP != 0 {
		t.Fatalf("unexpected event spawn stats: %+v", spawned)
	}
	for _, anchor := range MonsterRushAnchors() {
		rush, err := CombatTargetExperienceMetadata(root, anchor.MapID, target)
		if err != nil || rush.MaximumHP != MonsterRushHP || rush.Experience != 0 || rush.MobLevel != 78 {
			t.Fatalf("rush mob in %d: %+v, %v", anchor.MapID, rush, err)
		}
	}
	if _, err := CombatTargetExperienceMetadata(root, 100000000, target); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("rush tag outside its towns: %v", err)
	}
	target.MobID++
	if _, err := CombatTargetExperienceMetadata(root, town, target); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("rush tag on another template: %v", err)
	}
}
