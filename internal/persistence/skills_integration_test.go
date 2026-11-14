package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestSkillAllocationCastCooldownAndRecovery(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	a.Character.Level = 4
	a.Character.MaxMP = 20
	a.Character.MP = 20
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET level=4,base_max_mp=20,max_mp=20,mp=20 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	op := fmt.Sprintf("skill-allocate-operation-%d", a.CharacterID)
	allocation := domain.SkillAllocateRequest{Session: a, SkillID: 1001, OperationID: op}
	r, e := s.AllocateSkill(ctx, allocation)
	if e != nil || learned(r.Character, 1001).Level != 1 {
		t.Fatalf("allocate%+v %v", r, e)
	}
	if replay, e := s.AllocateSkill(ctx, allocation); e != nil || !replay.Replayed || learned(replay.Character, 1001).Level != 1 {
		t.Fatalf("replay%+v %v", replay, e)
	}
	a.Character = r.Character
	a.Character.HP = 10
	cast := domain.SkillCastRequest{Session: a, SkillID: 1001, ClientLevel: 1, OperationID: fmt.Sprintf("skill-cast-operation-%d", a.CharacterID)}
	r, e = s.CastSkill(ctx, cast)
	if e != nil || r.Character.MP != 15 || len(r.Character.SkillBuffs) != 1 {
		t.Fatalf("cast%+v %v", r, e)
	}
	if replay, e := s.CastSkill(ctx, cast); e != nil || !replay.Replayed || replay.Character.MP != 15 {
		t.Fatalf("cast replay%+v %v", replay, e)
	}
	a.Character = r.Character
	cast.Session = a
	cast.OperationID += "-twice"
	if _, e = s.CastSkill(ctx, cast); !errors.Is(e, domain.ErrAlreadyExists) {
		t.Fatalf("cooldown bypass%v", e)
	}
	cast.Session = b
	cast.OperationID += "-other"
	if _, e = s.CastSkill(ctx, cast); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("unlearned cast%v", e)
	}
	if _, e = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET next_tick=clock_timestamp()-interval '1 second' WHERE character_id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	r, e = s.TickSkillBuffs(ctx, a)
	if e != nil || r.Character.HP != 14 || !r.Changed {
		t.Fatalf("recovery tick%+v %v", r, e)
	}
	a.Character = r.Character
	r, e = s.TickSkillBuffs(ctx, a)
	if e != nil || r.Character.HP != 14 || r.Changed {
		t.Fatalf("duplicate tick%+v %v", r, e)
	}
	r, e = s.CancelSkill(ctx, a, 1001)
	if e != nil || len(r.Character.SkillBuffs) != 0 || len(r.Character.SkillCooldowns) != 1 || len(r.Expired) != 1 {
		t.Fatalf("cancel%+v %v", r, e)
	}
	if !r.Character.SkillCooldowns[0].ExpiresAt.After(time.Now()) {
		t.Fatal("cancel cleared cooldown")
	}
}
func TestSkillBeginnerPointPoolAndClientLevelValidation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1002, OperationID: fmt.Sprintf("skill-no-points-%d", a.CharacterID)}); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("level1pool%v", e)
	}
	a.Character.Level = 2
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET level=2 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	r, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1002, OperationID: fmt.Sprintf("skill-one-point-%d", a.CharacterID)})
	if e != nil {
		t.Fatal(e)
	}
	a.Character = r.Character
	if _, e = s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1001, OperationID: fmt.Sprintf("skill-over-point-%d", a.CharacterID)}); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("sharedpool%v", e)
	}
	if _, e = s.CastSkill(ctx, domain.SkillCastRequest{Session: a, SkillID: 1002, ClientLevel: 3, OperationID: fmt.Sprintf("skill-forged-level-%d", a.CharacterID)}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("forgedlevel%v", e)
	}
}
