package persistence

import (
	"testing"
)

func TestClearSkillBuffsDropsEveryRowAndKeepsCooldowns(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	for _, id := range []int64{a.CharacterID, b.CharacterID} {
		if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,1001003,1,'WDEF',10,clock_timestamp()+interval '1 minute'),($1,1001003,1,'MDEF',10,clock_timestamp()+interval '1 minute')`, id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skill_cooldowns(character_id,skill_id,expires_at) VALUES($1,1001003,clock_timestamp()+interval '1 minute')`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	r, e := s.ClearSkillBuffs(ctx, a)
	if e != nil || !r.Changed || len(r.Character.SkillBuffs) != 0 || len(r.Expired) != 2 || len(r.Character.SkillCooldowns) != 1 {
		t.Fatalf("clear%+v %v", r, e)
	}
	count := func(id int64) (n int) {
		if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM game_skill_buffs WHERE character_id=$1`, id).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count(a.CharacterID) != 0 || count(b.CharacterID) != 2 {
		t.Fatalf("rows left: cleared %d, other %d", count(a.CharacterID), count(b.CharacterID))
	}
	a.Character = r.Character
	if again, e := s.ClearSkillBuffs(ctx, a); e != nil || again.Changed {
		t.Fatalf("second clear%+v %v", again, e)
	}
}
