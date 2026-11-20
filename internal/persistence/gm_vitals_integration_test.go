package persistence

import (
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/domain"
)

func TestQAGMHealRestoresDerivedMaxima(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET vitals_model=1,base_max_hp=7535,base_max_mp=300,max_hp=6141,max_mp=300,hp=6141,mp=10 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.Character, err = s.GetCharacter(ctx, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-derived-vitals-heal-%d", a.CharacterID), Name: "heal"})
	if err != nil || !r.Changed || r.Character.MaxHP < 7535 || r.Character.HP != r.Character.MaxHP || r.Character.MP != r.Character.MaxMP {
		t.Fatalf("heal did not restore derived maxima %+v %v", r.Character, err)
	}
}

func TestQAGMJobParksOverflowingExtendedSP(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=2300,level=200,sp=597 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.Character, err = s.GetCharacter(ctx, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-mercedes-lv200-job-%d", a.CharacterID), Name: "job", Args: []int64{2310}})
	if err != nil || !r.Changed || r.Character.Job != 2310 || r.Character.SPBooks[domain.SkillBook(2300)] != 255 {
		t.Fatalf("Lv200 Mercedes job change %+v %v", r.Character.SPBooks, err)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Job != 2310 || loaded.SPBooks[domain.SkillBook(2300)] != 255 {
		t.Fatal("job change not durable", err)
	}
}

func TestQAGMBuffResetRemovesStatRows(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,35111004,20,'MECH_CHANGE',20,now()+interval '1 hour')`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.Character, err = s.GetCharacter(ctx, a.CharacterID); err != nil || len(a.Character.SkillBuffs) != 1 {
		t.Fatal("fixture row", a.Character.SkillBuffs, err)
	}
	r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-buffreset-mech-%d", a.CharacterID), Name: "buffreset", Args: []int64{107}})
	if err != nil || !r.Changed || len(r.Character.SkillBuffs) != 0 {
		t.Fatalf("buffreset %+v %v", r.Character.SkillBuffs, err)
	}
	a.Character = r.Character
	r, err = s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-buffreset-none-%d", a.CharacterID), Name: "buffreset", Args: []int64{107}})
	if err != nil || !r.Changed {
		t.Fatalf("reset without a row must still publish %v", err)
	}
	if _, err = s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-buffreset-bad-%d", a.CharacterID), Name: "buffreset", Args: []int64{256}}); err == nil {
		t.Fatal("out-of-range bit accepted")
	}
}

// A two-state stat takes its whole buff with it: resetting the riding bit of
// the mech leaves no half-mounted rows, and other buffs stay.
func TestQAGMBuffResetOfARideRemovesTheWholeMount(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES
		($1,35001002,10,'MONSTER_RIDING',1932016,now()+interval '1 hour'),
		($1,35001002,10,'ENHANCED_WATK',20,now()+interval '1 hour'),
		($1,35121007,30,'MAPLE_WARRIOR',15,now()+interval '1 hour')`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.Character, err = s.GetCharacter(ctx, a.CharacterID); err != nil || len(a.Character.SkillBuffs) != 3 {
		t.Fatal("fixture rows", a.Character.SkillBuffs, err)
	}
	r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-buffreset-ride-%d", a.CharacterID), Name: "buffreset", Args: []int64{251}})
	if err != nil || !r.Changed || len(r.Character.SkillBuffs) != 1 || r.Character.SkillBuffs[0].Stat != "MAPLE_WARRIOR" {
		t.Fatalf("ride reset left %+v %v", r.Character.SkillBuffs, err)
	}
}

func TestQAGMDebuffAppliesMobSkillStatus(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	a := grantQAActor(t, ctx, s, actors[0])
	poison, _ := buffstat.Lookup("POISON")
	r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-debuff-poison-%d", a.CharacterID), Name: "debuff", Args: []int64{int64(poison.Bit), 60}})
	if err != nil || !r.Changed || len(r.Character.SkillBuffs) != 1 {
		t.Fatalf("debuff %+v %v", r.Character.SkillBuffs, err)
	}
	b := r.Character.SkillBuffs[0]
	if b.Stat != "POISON" || b.ID != 125|1<<16 || b.Value != 10 || time.Until(b.ExpiresAt) < 50*time.Second {
		t.Fatalf("row %+v", b)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || len(loaded.SkillBuffs) != 1 {
		t.Fatal("debuff not durable", err)
	}
	a.Character = r.Character
	watk, _ := buffstat.Lookup("WATK")
	if _, err = s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-debuff-watk-%d", a.CharacterID), Name: "debuff", Args: []int64{int64(watk.Bit)}}); err == nil {
		t.Fatal("non-abnormal stat accepted")
	}
}
