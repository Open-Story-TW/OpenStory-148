package persistence

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestChangeSkillBuffsReplacesAndCancelsLearnedSkillRows(t *testing.T) {
	usePrivateSchema(t, "s0a")
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,1111002,10,0)`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	set := domain.SkillBuffChange{SkillID: 1111002, Level: 10, Duration: time.Minute, Stats: []domain.SkillBuffStat{{Stat: "COMBO", Value: 3}}}
	r, err := s.ChangeSkillBuffs(ctx, a, []domain.SkillBuffChange{set})
	if err != nil || len(r.Character.SkillBuffs) != 1 || r.Character.SkillBuffs[0].Value != 3 || r.Character.Revision != a.Character.Revision+1 {
		t.Fatalf("set %+v %v", r.Character.SkillBuffs, err)
	}
	a.Character = r.Character
	// Server-only counter, trigger cooldown and recovery.
	owl := domain.SkillBuffChange{SkillID: 1111002, Level: 10, Duration: time.Minute, Counter: 5, Cooldown: 10 * time.Second, HealHP: 1, Stats: []domain.SkillBuffStat{{Stat: "COMBO", Value: 3}}}
	if r, err = s.ChangeSkillBuffs(ctx, a, []domain.SkillBuffChange{owl}); err != nil || r.Character.SkillBuffs[0].Counter != 5 || len(r.Cooldowns) != 1 || r.Character.HP != min(r.Character.MaxHP, a.Character.HP+1) {
		t.Fatalf("counter/cooldown/heal %+v %+v %v", r.Character.SkillBuffs, r.Cooldowns, err)
	}
	if loaded, e := readCharacterSQL(ctx, s.db, a.CharacterID); e != nil || loaded.SkillBuffs[0].Counter != 5 {
		t.Fatalf("counter reload %+v %v", loaded.SkillBuffs, e)
	}
	a.Character = r.Character
	// Consume lowers another skill's stat in place.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := a.Character
	cr := domain.SkillResult{}
	if err = consumeBuffStats(ctx, tx, &c, &cr, []skills.StatValue{{Stat: "COMBO", Value: 2}}, time.Now()); err != nil || c.SkillBuffs[0].Value != 1 || len(cr.Updated) != 1 {
		t.Fatalf("consume %+v %v", c.SkillBuffs, err)
	}
	if err = consumeBuffStats(ctx, tx, &c, &cr, []skills.StatValue{{Stat: "COMBO", Value: 2}}, time.Now()); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("over-consume accepted: %v", err)
	}
	_ = tx.Rollback()
	set.Stats[0].Value = 4
	if r, err = s.ChangeSkillBuffs(ctx, a, []domain.SkillBuffChange{set}); err != nil || r.Character.SkillBuffs[0].Value != 4 || len(r.Expired) != 1 {
		t.Fatalf("grow %+v %+v %v", r.Character.SkillBuffs, r.Expired, err)
	}
	a.Character = r.Character
	if r, err = s.ChangeSkillBuffs(ctx, a, []domain.SkillBuffChange{{SkillID: 1111002, Level: 10, Cancel: true}}); err != nil || len(r.Character.SkillBuffs) != 0 {
		t.Fatalf("cancel %+v %v", r.Character.SkillBuffs, err)
	}
	a.Character = r.Character
	if _, err = s.ChangeSkillBuffs(ctx, a, []domain.SkillBuffChange{{SkillID: 1121000, Level: 1, Duration: time.Minute, Stats: []domain.SkillBuffStat{{Stat: "MAPLE_WARRIOR", Value: 1}}}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unlearned skill change accepted: %v", err)
	}
}
