package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// A combo-style buff grows on its owner's attacks and a hit-count buff ends.
func TestTriggersGrowAndCancel(t *testing.T) {
	Register(Handler{ID: 1111002, Kind: KindBuff, OnAttack: func(c *TriggerContext) *BuffChange {
		v, ok := c.Stat("COMBO")
		if !ok || c.Attack.SkillID == 1111002 {
			return nil
		}
		left := time.Until(c.Rows[0].ExpiresAt)
		return &BuffChange{Buff: &Buff{Duration: left, Stats: []StatValue{{Stat: "COMBO", Value: min(v+1, 6)}}}}
	}})
	Register(Handler{ID: 2000006, Kind: KindPassive, OnDamaged: func(c *TriggerContext) *BuffChange {
		if c.Damage < 10 {
			return nil
		}
		return &BuffChange{Cancel: true}
	}})
	now := time.Now()
	c := domain.Character{Skills: []domain.SkillState{{ID: 1111002, Level: 1}, {ID: 2000006, Level: 1}},
		SkillBuffs: []domain.SkillBuff{{ID: 1111002, Level: 1, Stat: "COMBO", Value: 2, ExpiresAt: now.Add(time.Minute)}}}
	got, _ := Triggers(c, now, &combat.AttackEvent{SkillID: 1001004}, 0, 0, nil, 0)
	if len(got) != 1 || got[0].SkillID != 1111002 || got[0].Stats[0] != (domain.SkillBuffStat{Stat: "COMBO", Value: 3}) || got[0].Duration <= 50*time.Second {
		t.Fatalf("combo %+v", got)
	}
	if got, _ = Triggers(c, now, nil, 5, 0, nil, 0); len(got) != 0 {
		t.Fatalf("small hit %+v", got)
	}
	if got, _ = Triggers(c, now, nil, 50, 0, nil, 0); len(got) != 1 || !got[0].Cancel || got[0].SkillID != 2000006 {
		t.Fatalf("damaged %+v", got)
	}
}

func TestPartyRecipientReviveTargetsOnlyTheDead(t *testing.T) {
	area := &Rect{Left: -100, Top: -50, Right: 100, Bottom: 50}
	src := domain.Character{X: 0, Y: 0, HP: 10}
	alive := domain.Character{X: 50, Y: 0, HP: 10}
	dead := domain.Character{X: 50, Y: 0, HP: 0}
	far := domain.Character{X: 500, Y: 0, HP: 0}
	buff := CastPlan{Area: area}
	revive := CastPlan{Area: area, Revive: true}
	if !PartyRecipient(buff, src, alive) || PartyRecipient(buff, src, dead) || PartyRecipient(revive, src, alive) || !PartyRecipient(revive, src, dead) || PartyRecipient(revive, src, far) {
		t.Fatal("recipient rules")
	}
}

func TestTriggerMobHitsWithoutBuffChange(t *testing.T) {
	Register(Handler{ID: 3120010, Kind: KindPassive, OnDamaged: func(c *TriggerContext) *BuffChange {
		return &BuffChange{MobHits: []combat.ServerHit{{ObjectID: c.Mob, Damage: c.Damage * 2}}}
	}})
	c := domain.Character{Skills: []domain.SkillState{{ID: 3120010, Level: 1}}}
	changes, hits := Triggers(c, time.Now(), nil, 40, 77, nil, 0)
	if len(changes) != 0 || len(hits) != 1 || hits[0].SkillID != 3120010 || hits[0].Hits[0] != (combat.ServerHit{ObjectID: 77, Damage: 80}) {
		t.Fatalf("counter %+v %+v", changes, hits)
	}
}

func TestTriggerMobsInRange(t *testing.T) {
	def, err := content.SkillDefinition("", 1301007) // lt -300,-200 rb 300,200
	if err != nil {
		t.Fatal(err)
	}
	c := &TriggerContext{Skill: def, Level: 1, Character: domain.Character{X: 100, Y: 0},
		Mobs: []TriggerMob{{ObjectID: 1, X: 350, Y: 10}, {ObjectID: 2, X: 450, Y: 0}, {ObjectID: 3, X: -150, Y: -100}, {ObjectID: 4, X: 0, Y: 0}}}
	if got := c.MobsInRange(2); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("mobs %v", got)
	}
}

func TestTriggerAttackerMaxHP(t *testing.T) {
	c := &TriggerContext{Mob: 7, Mobs: []TriggerMob{{ObjectID: 3, MaxHP: 10}, {ObjectID: 7, MaxHP: 5000}}}
	if m, ok := c.Attacker(); !ok || m.MaxHP/10 != 500 {
		t.Fatalf("attacker %+v %v", m, ok)
	}
}

func TestTicksRunOncePerPeriod(t *testing.T) {
	Register(Handler{ID: 1001003, Kind: KindPassive, TickEvery: 4 * time.Second, OnTick: func(c *TriggerContext) *BuffChange {
		return &BuffChange{HealMP: c.Level}
	}})
	c := domain.Character{Skills: []domain.SkillState{{ID: 1001003, Level: 3}}}
	last := map[int]time.Time{}
	now := time.Now()
	if got := Ticks(c, now, last); len(got) != 0 {
		t.Fatalf("first sight starts the period: %+v", got)
	}
	if got := Ticks(c, now.Add(3*time.Second), last); len(got) != 0 {
		t.Fatalf("early tick %+v", got)
	}
	if got := Ticks(c, now.Add(4*time.Second), last); len(got) != 1 || got[0].HealMP != 3 {
		t.Fatalf("tick %+v", got)
	}
	if got := Ticks(c, now.Add(5*time.Second), last); len(got) != 0 {
		t.Fatalf("repeat tick %+v", got)
	}
}

func TestLethalRevive(t *testing.T) {
	registerSoulStone()
	now := time.Now()
	c := domain.Character{Skills: []domain.SkillState{{ID: 22181003, Level: 1}}}
	if _, _, ok := Lethal(c, now, 999, 1); ok {
		t.Fatal("revived without the soul stone buff")
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 22181003, Level: 1, Stat: "SOUL_STONE", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	if ch, r, ok := Lethal(c, now, 999, 1); !ok || !ch.Cancel || ch.SkillID != 22181003 || !r.ReviveAtSpawn {
		t.Fatalf("revive %+v %+v %v", ch, r, ok)
	}
}

func TestLethalRunsForReceivedBuffs(t *testing.T) {
	registerSoulStone()
	now := time.Now()
	c := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 22181003, Level: 1, Stat: "SOUL_STONE", Value: 1, ExpiresAt: now.Add(time.Minute)}}}
	if ch, _, ok := Lethal(c, now, 999, 1); !ok || ch.SkillID != 22181003 || ch.Level != 1 {
		t.Fatalf("party member without the skill was not revived: %+v %v", ch, ok)
	}
}

func registerSoulStone() {
	if _, ok := Lookup(22181003); ok {
		return
	}
	Register(Handler{ID: 22181003, Kind: KindBuff, OnLethal: func(c *TriggerContext) *BuffChange {
		if len(c.Rows) == 0 {
			return nil
		}
		return &BuffChange{Cancel: true, ReviveHPPercent: c.Value("x"), ReviveAtSpawn: true}
	}})
}
