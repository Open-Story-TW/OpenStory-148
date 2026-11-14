package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"testing"
)

func TestSkillAttackCostRewardReplayAndBinding(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	a.Character.Level = 2
	a.Character.MP = 10
	a.Character.MaxMP = 10
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET level=2,base_max_mp=10,max_mp=10,mp=10 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	allocated, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1000, OperationID: fmt.Sprintf("snail-learn-operation-%d", a.CharacterID)})
	if e != nil {
		t.Fatal(e)
	}
	a.Character = allocated.Character
	catalog, e := content.DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	mob, e := catalog.Mob(9300018)
	if e != nil {
		t.Fatal(e)
	}
	q := domain.SkillAttackRequest{Session: a, SkillID: 1000, ClientTick: 7, OperationID: fmt.Sprintf("snail-attack-operation-%d", a.CharacterID), SpawnOperationID: fmt.Sprintf("mob-snail-spawn-operation-%d", a.CharacterID), TargetObjectID: 1000001, TargetMobID: mob.ID, Damage: 1, Defeated: true, Experience: mob.EXP}
	r, e := s.ApplySkillAttack(ctx, q)
	if e != nil || r.Character.MP != 7 || r.Character.EXP != mob.EXP {
		t.Fatalf("attack %+v %v", r, e)
	}
	replay, e := s.ApplySkillAttack(ctx, q)
	if e != nil || !replay.Replayed || replay.Character.MP != 7 || replay.Character.EXP != mob.EXP {
		t.Fatalf("replay %+v %v", replay, e)
	}
	for _, changed := range []domain.SkillAttackRequest{func() domain.SkillAttackRequest { x := q; x.OperationID += "-new"; return x }(), func() domain.SkillAttackRequest { x := q; x.TargetObjectID++; return x }(), func() domain.SkillAttackRequest { x := q; x.Damage++; return x }()} {
		if _, e = s.ApplySkillAttack(ctx, changed); !errors.Is(e, domain.ErrForbidden) {
			t.Fatalf("altered same tick accepted: %v", e)
		}
	}
	a.Character = r.Character
	q.Session = a
	q.ClientTick++
	q.OperationID += "-spawn-again"
	if _, e = s.ApplySkillAttack(ctx, q); !errors.Is(e, domain.ErrAlreadyExists) {
		t.Fatalf("same spawn rewarded twice: %v", e)
	}
	loaded, e := readCharacterSQL(ctx, s.db, a.CharacterID)
	if e != nil || loaded.MP != 7 || loaded.EXP != mob.EXP || loaded.Revision != a.Character.Revision {
		t.Fatalf("failed reward rolled back improperly: %+v %v", loaded, e)
	}
	q.Session = b
	q.OperationID += "-unlearned"
	q.SpawnOperationID += "-other"
	if _, e = s.ApplySkillAttack(ctx, q); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("unlearned skill accepted: %v", e)
	}
	miss := domain.SkillAttackRequest{Session: a, SkillID: 1000, ClientTick: 9, OperationID: fmt.Sprintf("snail-miss-operation-%d", a.CharacterID)}
	r, e = s.ApplySkillAttack(ctx, miss)
	if e != nil || r.Character.MP != 4 || r.Character.EXP != mob.EXP {
		t.Fatalf("miss cost: %+v %v", r, e)
	}
	miss.Session.Character = r.Character
	miss.ClientTick++
	miss.OperationID += "-second"
	r, e = s.ApplySkillAttack(ctx, miss)
	if e != nil || r.Character.MP != 1 {
		t.Fatalf("second miss: %+v %v", r, e)
	}
	miss.Session.Character = r.Character
	miss.ClientTick++
	miss.OperationID += "-nomp"
	if _, e = s.ApplySkillAttack(ctx, miss); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("insufficient MP accepted: %v", e)
	}
}
