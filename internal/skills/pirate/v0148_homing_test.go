package pirate

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestContinueAimMarksTargetWithHomingBeacon(t *testing.T) {
	h, ok := skills.Lookup(5221015)
	if !ok || h.OnAttack == nil {
		t.Fatalf("5221015 missing OnAttack homingBeacon: %+v", h)
	}
	ch := domain.Character{Job: 522, Skills: []domain.SkillState{{ID: 5221015, Level: 20}}}
	change := h.OnAttack(&skills.TriggerContext{
		Character: ch,
		Skill:     content.Skill{ID: 5221015},
		Level:     20,
		Now:       time.Unix(100, 0),
		Attack:    &combat.AttackEvent{SkillID: 5221015, Targets: []int32{4242}},
	})
	if change == nil || change.Buff == nil || len(change.Buff.Stats) != 1 ||
		change.Buff.Stats[0].Stat != "HOMING_BEACON" || change.Buff.Stats[0].Value != 4242 {
		t.Fatalf("5221015 homing mark = %+v", change)
	}
}
