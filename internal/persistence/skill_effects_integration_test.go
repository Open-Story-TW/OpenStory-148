package persistence

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// Costs and side effects a cast plan can carry besides buff rows.
func TestCastPlanCostsHealDispelResetAndInfinity(t *testing.T) {
	usePrivateSchema(t, "s0a")
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET hp=10,mp=50,base_max_mp=100,max_mp=100 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	c, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || len(c.Items) != 1 || c.Items[0].TemplateID != 2000000 || c.Items[0].Quantity != 5 {
		t.Fatalf("fixture %+v %v", c.Items, err)
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r := domain.SkillResult{}
	plan := skills.CastPlan{MPCost: 20, HPCost: 3, Items: []skills.ItemCost{{ItemID: 2000000, Quantity: 2}}, Cooldown: time.Minute}
	if err = payCastCost(ctx, tx, &c, &r, 1001, plan, now); err != nil || c.MP != 30 || c.HP != 7 || c.Items[0].Quantity != 3 || len(r.Inventory) != 1 || r.Inventory[0].Quantity != 3 || len(c.SkillCooldowns) != 1 {
		t.Fatalf("cost %+v %+v %v", c, r.Inventory, err)
	}
	if err = payCastCost(ctx, tx, &c, &r, 1002, skills.CastPlan{Items: []skills.ItemCost{{ItemID: 2000000, Quantity: 4}}}, now); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("missing items accepted: %v", err)
	}
	if err = payCastCost(ctx, tx, &c, &r, 1002, skills.CastPlan{HPCost: 7}, now); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("lethal HP cost accepted: %v", err)
	}
	// INFINITY makes MP costs free.
	c.SkillBuffs = append(c.SkillBuffs, domain.SkillBuff{ID: 2121004, Level: 1, Stat: "INFINITY", Value: 1, ExpiresAt: now.Add(time.Minute)})
	if err = payCastCost(ctx, tx, &c, &r, 1002, skills.CastPlan{MPCost: 1000}, now); err != nil || c.MP != 30 {
		t.Fatalf("infinity %d %v", c.MP, err)
	}
	c.SkillBuffs = nil
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,128,1,'SEAL',1,$2),($1,1001003,1,'WDEF',10,$2)`, c.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 128, Level: 1, Stat: "SEAL", Value: 1, ExpiresAt: now.Add(time.Minute)}, {ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: now.Add(time.Minute)}}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_skill_cooldowns(character_id,skill_id,expires_at) VALUES($1,5121010,$2),($1,5821010,$2)`, c.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	expiry := now.Add(time.Minute)
	c.SkillCooldowns = append(c.SkillCooldowns,
		domain.SkillCooldown{ID: 5121010, ExpiresAt: expiry},
		domain.SkillCooldown{ID: 5821010, ExpiresAt: expiry})
	r = domain.SkillResult{}
	effects := skills.CastPlan{Dispel: true, ResetCooldowns: true, KeepCooldowns: []int{5121010, 5821010}, Heal: &skills.Heal{HP: 5, MPPercent: 10}}
	if err = applyCastEffects(ctx, tx, &c, &r, 5121010, effects, nil); err != nil {
		t.Fatal(err)
	}
	if len(c.SkillBuffs) != 1 || c.SkillBuffs[0].Stat != "WDEF" || len(r.Expired) != 1 || r.Expired[0].Stat != "SEAL" {
		t.Fatalf("dispel %+v %+v", c.SkillBuffs, r.Expired)
	}
	if len(c.SkillCooldowns) != 2 || c.SkillCooldowns[0].ID != 5121010 || c.SkillCooldowns[1].ID != 5821010 || len(r.ResetCooldowns) != 1 || r.ResetCooldowns[0].ID != 1001 || c.HP != min(c.MaxHP, 12) || c.MP != min(c.MaxMP, 30+c.MaxMP/10) {
		t.Fatalf("reset/heal %+v hp %d mp %d", r.ResetCooldowns, c.HP, c.MP)
	}
	var left int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM game_skill_cooldowns WHERE character_id=$1`, c.ID).Scan(&left); err != nil || left != 2 {
		t.Fatalf("cooldown rows %d %v", left, err)
	}
	for _, id := range []int{5121010, 5821010} {
		var stored time.Time
		err = tx.QueryRowContext(ctx, `SELECT expires_at FROM game_skill_cooldowns WHERE character_id=$1 AND skill_id=$2`, c.ID, id).Scan(&stored)
		if err != nil || !stored.Equal(expiry.Truncate(time.Microsecond)) {
			t.Fatalf("cooldown %d expiry %v, want %v: %v", id, stored, expiry, err)
		}
	}
	if err = applyCastEffects(ctx, tx, &c, &r, 1, skills.CastPlan{Heal: &skills.Heal{HPPercent: 101}}, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("over-100 percent heal accepted: %v", err)
	}
}
