package common

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestV148AngelOriginalRelatedEffect(t *testing.T) {
	now := time.Unix(100, 0)
	defer skills.SetBuffApplier(nil)
	for _, sample := range []struct{ parent, related, ring, attack int }{
		{30001085, 30000086, 1112585, 5},
		{30011085, 30010086, 1112585, 5},
		{30011090, 30010091, 1112594, 5},
		{50001087, 50000088, 1112586, 10},
		{20031179, 20030180, 1112663, 12},
	} {
		h, ok := skills.Lookup(sample.parent)
		if !ok || h.Attack.Summon.AssistType != 2 || h.Attack.Summon.Sub == nil {
			t.Fatalf("parent %d lacks original assistance", sample.parent)
		}
		def, err := content.ConfiguredSkillDefinition(sample.related)
		if err != nil {
			t.Fatal(err)
		}
		v, err := def.Values(1)
		if err != nil {
			t.Fatal(err)
		}
		c := combat.SubSummonContext{Character: domain.Character{ID: 1, Equipment: []domain.Equipment{{ItemID: sample.ring, Slot: -12}}},
			SummonSkill: sample.parent, SummonLvl: 1, SubSkill: sample.related, SubLevel: 1, SubValues: v, Now: now}
		if got := skills.EquipmentRelatedSkillLevel(c.Character, sample.related, now); got != 1 {
			t.Fatalf("ring-related level %d = %d", sample.related, got)
		}
		if _, ok := skills.CastLevel(c.Character, sample.related, sample.related, 1, now); ok {
			t.Fatal("related effect admitted as an unlearned direct cast")
		}
		var applied domain.SkillBuffChange
		skills.SetBuffApplier(func(owner int64, ch domain.SkillBuffChange) {
			if owner != 1 {
				t.Fatal("wrong related owner")
			}
			applied = ch
		})
		res, err := h.Attack.Summon.Sub(c)
		if err != nil || res.Action != 8 || res.Apply == nil {
			t.Fatalf("related %d: %+v %v", sample.related, res, err)
		}
		res.Apply()
		if applied.SkillID != sample.related || applied.Level != 1 || applied.Duration != 600*time.Second || len(applied.Stats) != 2 {
			t.Fatalf("wrong original related buff: %+v", applied)
		}
		for _, stat := range applied.Stats {
			if stat.Value != sample.attack {
				t.Fatalf("wrong original attack bonus: %+v", applied)
			}
		}
		c.LastSub = now.Add(-599 * time.Second)
		if _, err := h.Attack.Summon.Sub(c); err == nil {
			t.Fatal("early related request admitted")
		}
		c.LastSub = now.Add(-600 * time.Second)
		if _, err := h.Attack.Summon.Sub(c); err != nil {
			t.Fatal(err)
		}
		c.Character.Equipment[0].Slot = 1
		if _, err := h.Attack.Summon.Sub(c); err == nil {
			t.Fatal("bag ring admitted")
		}
	}
}
