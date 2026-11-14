package persistence

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// S0b generic family: MP, projectiles and per-line damage settle in one
// transaction; over-cost, unlearned and forged lines roll back.
func TestStagedCombatSkillFamilyPaysAmmoAndBoundsCost(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	id := actors[0].CharacterID
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=300 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,3001005,1,0)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,5,2060000,3)`, id); err != nil {
		t.Fatal(err)
	}
	var err error
	if actors[0].Character, err = s.GetCharacter(ctx, id); err != nil {
		t.Fatal(err)
	}
	actors = actors[:1]
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mob, err := catalog.Mob(9300018)
	if err != nil {
		t.Fatal(err)
	}
	mobHP := mob.HP
	build := func(op string, cost domain.CombatCost, applied ...int) domain.CombatAttackRequest {
		q := domain.CombatAttackRequest{Session: actors[0], OperationID: fmt.Sprintf("s0b-%s-%d", op, time.Now().UnixNano()), ClientTick: uint32(time.Now().UnixNano()), Family: domain.CombatSkill, SkillID: 3001005, LinesPerTarget: len(applied), Members: actors, Cost: &cost}
		sum := 0
		var lines []domain.CombatDamageLine
		for _, a := range applied {
			lines = append(lines, domain.CombatDamageLine{Raw: uint32(a), Applied: a})
			sum += a
		}
		// A full-HP target: contributions equal this attack's damage.
		q.Targets = []domain.CombatTarget{{ObjectID: 1000001, MobID: 9300018, SpawnOperationID: "mob-" + q.OperationID, BeforeHP: mobHP, Damage: sum, Lines: lines,
			Contributions: []domain.CombatContribution{{CharacterID: id, SessionID: actors[0].SessionID, Generation: actors[0].Generation, Damage: int64(sum)}}}}
		return q
	}
	q := build("ok", domain.CombatCost{MP: 8, Level: 1, AmmoItemID: 2060000, AmmoSlot: 5, Ammo: 2}, 3, 4)
	result, err := s.ApplyCombatAttack(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ammo == nil || result.Ammo.Quantity != 1 || result.Ammo.Slot != 5 {
		t.Fatalf("ammo result %+v", result.Ammo)
	}
	c, _ := s.GetCharacter(ctx, id)
	if c.MP != actors[0].Character.MP-8 {
		t.Fatalf("mp %d", c.MP)
	}
	actors[0].Character = c
	for _, bad := range []domain.CombatCost{
		{MP: 9, Level: 1}, // above WZ mpCon
		{MP: 8, Level: 2}, // not the learned level
		{MP: 8, Level: 1, AmmoItemID: 2060000, AmmoSlot: 5, Ammo: 2}, // only one arrow left
		{MP: 8, Level: 1, AmmoItemID: 2000000, AmmoSlot: 5, Ammo: 1}, // not a projectile at that slot
	} {
		b := build("bad", bad, 1)
		if _, err = s.ApplyCombatAttack(ctx, b); err == nil || errors.Is(err, domain.ErrStaleOwner) {
			t.Fatalf("accepted bad cost %+v", bad)
		}
	}
	forged := build("forged", domain.CombatCost{MP: 8, Level: 1}, 5)
	forged.Targets[0].Lines[0].Raw = 4 // applied above the client claim
	if _, err = s.ApplyCombatAttack(ctx, forged); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("forged line %v", err)
	}
	after, _ := s.GetCharacter(ctx, id)
	if after.MP != c.MP {
		t.Fatal("rejected attacks charged MP")
	}
}
