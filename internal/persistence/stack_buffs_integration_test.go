package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestStackBuffsKeepsOtherSourcesAndCancelsOne(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	defer domain.SetStackBuffsForTest(true)()
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, e := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,7,2002000,2)`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	// Another source already holds avoidability as an indie entry.
	if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,4001003,1,'ANGEL_AVOID',5,clock_timestamp()+interval '1 minute')`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	var e error
	if a.Character, e = s.GetCharacter(ctx, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	r, e := s.ApplyItemUse(ctx, domain.UseItemRequest{Session: a, ClientTick: 91001, Slot: 7, TemplateID: 2002000})
	if e != nil || len(r.Buffs) != 1 || r.Buffs[0].ID != -2002000 || r.Buffs[0].Stat != "ANGEL_AVOID" || r.Buffs[0].Value != 100 || len(r.Replaced) != 0 {
		t.Fatalf("use %+v %v", r, e)
	}
	if len(r.Character.SkillBuffs) != 2 {
		t.Fatalf("live rows %+v", r.Character.SkillBuffs)
	}
	// The same item again refreshes its own row and keeps the other source.
	a.Character = r.Character
	r, e = s.ApplyItemUse(ctx, domain.UseItemRequest{Session: a, ClientTick: 91002, Slot: 7, TemplateID: 2002000})
	if e != nil || len(r.Replaced) != 1 || r.Replaced[0].ID != -2002000 || len(r.Character.SkillBuffs) != 2 {
		t.Fatalf("reuse %+v %v", r, e)
	}
	var n, sum int
	if e = s.db.QueryRowContext(ctx, `SELECT count(*),sum(value) FROM game_skill_buffs WHERE character_id=$1 AND stat='ANGEL_AVOID'`, a.CharacterID).Scan(&n, &sum); e != nil || n != 2 || sum != 105 {
		t.Fatalf("saved rows %d %d %v", n, sum, e)
	}
	loaded, e := s.GetCharacter(ctx, a.CharacterID)
	if e != nil || len(loaded.SkillBuffs) != 2 {
		t.Fatalf("reload %+v %v", loaded.SkillBuffs, e)
	}
	// Cancelling the item's icon removes only the item's entry.
	a.Character = r.Character
	c, e := s.CancelSkill(ctx, a, -2002000)
	if e != nil || len(c.Expired) != 1 || c.Expired[0].ID != -2002000 || len(c.Character.SkillBuffs) != 1 || c.Character.SkillBuffs[0].ID != 4001003 {
		t.Fatalf("cancel %+v %v", c, e)
	}
	if e = s.db.QueryRowContext(ctx, `SELECT count(*),sum(value) FROM game_skill_buffs WHERE character_id=$1`, a.CharacterID).Scan(&n, &sum); e != nil || n != 1 || sum != 5 {
		t.Fatalf("after cancel %d %d %v", n, sum, e)
	}
}
