package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// Replace removes other skills' rows before applying; Retime moves the
// caster's other buff expiry.
func TestCastPlanReplaceAndRetime(t *testing.T) {
	usePrivateSchema(t, "s0a")
	ctx, s, sessions := inventoryFixture(t)
	c, err := readCharacterSQL(ctx, s.db, sessions[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r := domain.SkillResult{}
	dark, _ := skillBuffRows(32001003, 1, &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "DARK_AURA", Value: 1}}}, now)
	if err = applyCastEffects(ctx, tx, &c, &r, 32001003, skills.CastPlan{}, dark); err != nil {
		t.Fatal(err)
	}
	blue, _ := skillBuffRows(32111012, 1, &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "BLUE_AURA", Value: 1}}}, now)
	r = domain.SkillResult{}
	if err = applyCastEffects(ctx, tx, &c, &r, 32111012, skills.CastPlan{Replace: []int{32001003, 32101003, 32111012}}, blue); err != nil || len(c.SkillBuffs) != 1 || c.SkillBuffs[0].Stat != "BLUE_AURA" || len(r.Expired) != 1 {
		t.Fatalf("replace %+v %+v %v", c.SkillBuffs, r.Expired, err)
	}
	r = domain.SkillResult{}
	if err = retimeSkillBuffs(ctx, tx, &c, &r, []skills.SkillDuration{{SkillID: 32111012, Duration: 10 * time.Second}}, now); err != nil || len(r.Updated) != 1 || c.SkillBuffs[0].ExpiresAt.Sub(now) != 10*time.Second {
		t.Fatalf("retime %+v %v", c.SkillBuffs, err)
	}
}
