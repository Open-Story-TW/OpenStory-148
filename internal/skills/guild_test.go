package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestGuildSkillsUseWZRanksForCurrentMembersUntilExpiry(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	c := domain.Character{STR: 500, DEX: 100, INT: 160, LUK: 100, Guild: domain.GuildTag{ID: 4}}
	c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1302000, ACC: 120, AVOID: 150}}
	base := legacyPlayerStats(c, now)
	for id := 91000000; id <= 91000005; id++ {
		c.GuildSkills = append(c.GuildSkills, domain.GuildSkillState{SkillID: id, Level: 1, ExpiresAt: expires.UnixMilli()})
	}
	c.GuildSkills = append(c.GuildSkills, domain.GuildSkillState{SkillID: 91000006, Level: 1, ExpiresAt: -1})
	bonus := Bonuses(c, now)
	want := Bonus{ACCPercent: 2, GuildAVOIDPercent: 2, EXP: 2, GuildWDEFPercent: 5, GuildMDEFPercent: 5,
		WATK: 4, MATK: 4, Speed: 5, Jump: 5, Critical: 2}
	if bonus != want {
		t.Fatalf("guild WZ rank 1 bonuses = %+v, want %+v", bonus, want)
	}
	stats := legacyPlayerStats(c, now)
	if stats.WDEF != base.WDEF+base.WDEF/20 || stats.MDEF != base.MDEF+base.MDEF/20 ||
		stats.Accuracy != base.Accuracy+base.Accuracy/50 || stats.Avoid != base.Avoid+base.Avoid/50 ||
		stats.Speed != base.Speed+5 || stats.Jump != base.Jump+5 || stats.WATK != base.WATK+4 ||
		stats.MATK != base.MATK+4 || stats.CritRate != base.CritRate+2 {
		t.Fatalf("guild stats = %+v, baseline %+v", stats, base)
	}
	if got := Bonuses(c, expires); got != (Bonus{}) {
		t.Fatalf("expired guild skill supplied bonus %+v", got)
	}
	c.Guild = domain.GuildTag{}
	if got := Bonuses(c, now); got != (Bonus{}) {
		t.Fatalf("former member retained guild skill bonus %+v", got)
	}
}
