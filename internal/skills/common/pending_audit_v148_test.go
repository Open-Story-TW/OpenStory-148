package common_test

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestPharaohWrathAndGiantAttacksAndGuildPassivesV148(t *testing.T) {
	// 1) Pharaoh's Wrath (x1020): melee attack with 100% target max HP ceiling
	//    on both regular and boss mobs (v148 0x938AA8 -> 0x92D0E9, 0x711FC2).
	for _, id := range beginnerIDs(1020) {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Melee) || h.Attack.Ammo != combat.AmmoNone || h.Attack.MaxDamage == nil {
			t.Fatalf("pharaoh skill %d: ok=%v attack=%+v", id, ok, h.Attack)
		}
		if got := h.Attack.MaxDamage(combat.DamageContext{Mob: content.Mob{HP: 500000}}); got != 500000 {
			t.Fatalf("pharaoh %d normal max damage = %d, want 500000", id, got)
		}
		if got := h.Attack.MaxDamage(combat.DamageContext{Mob: content.Mob{HP: 500000, Boss: true}}); got != 500000 {
			t.Fatalf("pharaoh %d boss max damage = %d, want 500000", id, got)
		}
	}

	// 2) Giant Potion jump/swing attacks (x1092, x1094): melee attacks (0x93B3B8/0x93B3C0).
	for _, id := range beginnerIDs(1092, 1094) {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Melee) || h.Attack.Allows(combat.Body) || h.Attack.Ammo != combat.AmmoNone {
			t.Fatalf("giant melee skill %d: ok=%v attack=%+v", id, ok, h.Attack)
		}
	}

	// 3) Giant Potion body-contact attack (x1095): body attack (0x925CDE -> 0x9232A1).
	for _, id := range beginnerIDs(1095) {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Body) || h.Attack.Allows(combat.Melee) || h.Attack.Ammo != combat.AmmoNone {
			t.Fatalf("giant body skill %d: ok=%v attack=%+v", id, ok, h.Attack)
		}
	}

	// 4) Guild skills (91000000..91000006): registered as passive markers whose
	//    bonuses come solely from c.GuildSkills via skills.guildBonuses (no double-apply).
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	c := domain.Character{
		Guild: domain.GuildTag{ID: 7},
		GuildSkills: []domain.GuildSkillState{
			{SkillID: 91000003, Level: 2, ExpiresAt: now.Add(time.Hour).UnixMilli()},
		},
		Skills: []domain.SkillState{
			{ID: 91000003, Level: 2},
		},
	}
	for id := 91000000; id <= 91000006; id++ {
		h, ok := skills.Lookup(id)
		if !ok || h.Kind != skills.KindPassive || h.Passive != nil || h.Cast != nil {
			t.Fatalf("guild skill %d: ok=%v kind=%v passive=%v cast=%v", id, ok, h.Kind, h.Passive != nil, h.Cast != nil)
		}
	}
	if b := skills.Bonuses(c, now); b.WATK != 8 || b.MATK != 8 {
		t.Fatalf("guild skill 91000003 bonus with duplicate c.Skills entry = %+v, want WATK=8 MATK=8", b)
	}
}
