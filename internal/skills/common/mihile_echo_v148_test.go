package common_test

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"os"
	"testing"
	"time"
)

func TestMihileEchoOriginalV148Alias(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("requires exported original v148 WZ")
	}
	def, err := content.SkillDefinition(root, 50001215)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(1)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := skills.Lookup(50001215)
	if !ok || h.Cast == nil {
		t.Fatal("original Mihile Echo alias missing")
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Values: v, Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Self == nil || p.Party != nil || p.MPCost != 30 || p.Cooldown != 7200*time.Second || p.Self.Duration != 2400*time.Second || stats(p.Self)["ECHO_OF_HERO"] != 4 {
		t.Fatalf("bad original Echo plan %+v buff%+v", p, p.Self)
	}
}

func TestMihileKnightsWatchV148(t *testing.T) {
	def, err := content.ConfiguredSkillDefinition(50001214)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(1)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := skills.Lookup(50001214)
	if !ok || h.Cast == nil {
		t.Fatal("Mihile Knight's Watch 50001214 missing")
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Values: v, Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Self == nil || p.Party != nil || p.Cooldown != 180*time.Second || p.Self.Duration != 90*time.Second || stats(p.Self)["STANCE"] != 100 {
		t.Fatalf("bad Knight's Watch plan %+v buff%+v", p, p.Self)
	}
}
