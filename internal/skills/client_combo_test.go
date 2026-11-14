package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func init() {
	Register(Handler{ID: 21000000, Kind: KindPassive, OnClientCombo: func(c *TriggerContext) *BuffChange {
		return &BuffChange{Buff: &Buff{Duration: time.Minute, Stats: []StatValue{{Stat: "ARAN_COMBO", Value: 1}}, Counter: 1}}
	}})
}

// ClientComboTriggers runs only OnClientCombo (RecvAranCombo), not OnAttack.
func TestClientComboTriggers(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 2100, Skills: []domain.SkillState{{ID: 21000000, Level: 1}}}
	got := ClientComboTriggers(c, now)
	if len(got) != 1 || got[0].SkillID != 21000000 {
		t.Fatalf("combo changes %+v", got)
	}
	if out, _ := Triggers(c, now, nil, 1, 0, nil, 0); len(out) != 0 {
		t.Fatalf("OnDamaged ran the combo hook %+v", out)
	}
}
