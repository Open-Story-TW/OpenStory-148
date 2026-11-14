package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestItemBuffSavesRowsReplacesAndCancels(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	defer domain.SetStackBuffsForTest(false)() // asserts the stock replacement
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, e := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,7,2002000,2)`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	// A skill row on the same stat is replaced by the pill.
	if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,4001003,1,'AVOID',5,clock_timestamp()+interval '1 minute')`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	var e error
	if a.Character, e = s.GetCharacter(ctx, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	r, e := s.ApplyItemUse(ctx, domain.UseItemRequest{Session: a, ClientTick: 91001, Slot: 7, TemplateID: 2002000})
	if e != nil || len(r.Buffs) != 1 || r.Buffs[0].ID != -2002000 || r.Buffs[0].Stat != "AVOID" || r.Buffs[0].Value != 100 || len(r.Replaced) != 1 || r.Replaced[0].ID != 4001003 || r.QuantityAfter != 1 {
		t.Fatalf("use%+v %v", r, e)
	}
	if left := time.Until(r.Buffs[0].ExpiresAt); left < 170*time.Second || left > 181*time.Second {
		t.Fatalf("pill lasts %v", left)
	}
	var id, value int
	if e = s.db.QueryRowContext(ctx, `SELECT skill_id,value FROM game_skill_buffs WHERE character_id=$1 AND stat='AVOID'`, a.CharacterID).Scan(&id, &value); e != nil || id != -2002000 || value != 100 {
		t.Fatalf("saved row %d %d %v", id, value, e)
	}
	// A fresh load (relog) reads the item buff back.
	loaded, e := s.GetCharacter(ctx, a.CharacterID)
	if e != nil || len(loaded.SkillBuffs) != 1 || loaded.SkillBuffs[0].ID != -2002000 {
		t.Fatalf("reload %+v %v", loaded.SkillBuffs, e)
	}
	a.Character = r.Character
	c, e := s.CancelSkill(ctx, a, -2002000)
	if e != nil || len(c.Character.SkillBuffs) != 0 || len(c.Expired) != 1 {
		t.Fatalf("cancel%+v %v", c, e)
	}
}
