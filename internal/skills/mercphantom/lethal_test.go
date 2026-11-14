package mercphantom

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestFinalFeintRevivesOffCooldown(t *testing.T) {
	def, _ := content.SkillDefinition("", 24111002)
	v, _ := def.Values(20)
	h, _ := skills.Lookup(24111002)
	now := time.Unix(100, 0)
	c := domain.Character{Job: 2412}
	got := h.OnLethal(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Character: c, Damage: 9999, Now: now})
	if got == nil || got.ReviveHPPercent != 100 || got.ReviveAtSpawn || got.Cooldown != 300*time.Second {
		t.Fatalf("final feint %+v", got)
	}
	c.SkillCooldowns = []domain.SkillCooldown{{ID: 24111002, ExpiresAt: now.Add(time.Minute)}}
	if got := h.OnLethal(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Character: c, Damage: 9999, Now: now}); got != nil {
		t.Errorf("revived on cooldown %+v", got)
	}
}
