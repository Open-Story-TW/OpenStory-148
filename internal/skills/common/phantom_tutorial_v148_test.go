package common_test

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"os"
	"testing"
)

func TestOriginalV148PhantomLureHasNoServerBuffOrAttack(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("requires exported original v148 WZ")
	}
	def, err := content.SkillDefinition(root, 20031212)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(1)
	if err != nil {
		t.Fatal(err)
	}
	if def.MaxLevel != 1 || v["mpCon"] != 0 || v["time"] != 420 || v["x"] != 200 {
		t.Fatalf("original tutorial values changed: %+v", v)
	}
	h, ok := skills.Lookup(20031212)
	if !ok || h.Cast == nil || h.Attack != nil || h.Kind != skills.KindSpecial {
		t.Fatal("native tutorial acquisition missing")
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Values: v, Level: 1})
	if err != nil || p.MPCost != 0 || p.Self != nil || p.Party != nil {
		t.Fatalf("local guard lure must not install the WZ time as a server buff: %+v, %v", p, err)
	}
}
