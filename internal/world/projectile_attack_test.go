package world

import (
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/domain"
)

func TestProjectileAttackIsTheNamedHeldStack(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Item.wz", "Consume")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	star := `<imgdir name="0207.img"><imgdir name="02070000"><imgdir name="info"><int name="slotMax" value="500"/><int name="incPAD" value="15"/></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "0207.img.xml"), []byte(star), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	c := domain.Character{Items: []domain.InventoryItem{
		{InvType: 2, Slot: 3, TemplateID: 2070000, Quantity: 10},
		{InvType: 2, Slot: 4, TemplateID: 2070000, Quantity: 0},
		{InvType: 2, Slot: 5, TemplateID: 2000000, Quantity: 10},
	}}
	if got := projectileAttack(c, 3); got != 15 {
		t.Fatalf("held stars: %d", got)
	}
	for _, slot := range []int{4, 5, 9} {
		if got := projectileAttack(c, slot); got != 0 {
			t.Fatalf("slot %d must add nothing: %d", slot, got)
		}
	}
}

func TestZenProjectileDamageDoesNotRepeatBuiltInAttack(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Item.wz", "Consume")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	bullets := `<imgdir name="0233.img">
		<imgdir name="02330000"><imgdir name="info"><int name="incPAD" value="10"/></imgdir></imgdir>
		<imgdir name="02330013"><imgdir name="info"><int name="incPAD" value="20"/></imgdir></imgdir>
	</imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "0233.img.xml"), []byte(bullets), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	c := domain.Character{Job: 572, Items: []domain.InventoryItem{
		{InvType: 2, Slot: 3, TemplateID: 2330013, Quantity: 10},
		{InvType: 2, Slot: 4, TemplateID: 2330000, Quantity: 10},
	}}
	if got := projectileAttack(c, 3); got != 0 {
		t.Fatalf("duplicate built-in projectile attack = %d", got)
	}
	if got := projectileAttack(c, 4); got != 10 {
		t.Fatalf("different held projectile attack = %d, want 10", got)
	}
	c.Job = 522
	if got := projectileAttack(c, 3); got != 20 {
		t.Fatalf("ordinary job's held projectile attack = %d, want 20", got)
	}
}
