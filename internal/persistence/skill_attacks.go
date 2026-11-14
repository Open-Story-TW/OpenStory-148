package persistence

import (
	"context"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"time"
)

var _ domain.SkillAttackRepository = (*Store)(nil)

func (s *Store) ApplySkillAttack(ctx context.Context, req domain.SkillAttackRequest) (domain.SkillAttackResult, error) {
	if req.SkillID != 1000 || len(req.OperationID) < 16 || len(req.OperationID) > 128 || req.TargetObjectID < 0 || req.TargetMobID < 0 || req.Damage < 0 || req.Experience < 0 || (!req.Defeated && req.Experience != 0) {
		return domain.SkillAttackResult{}, domain.ErrInvalidInput
	}
	if req.TargetObjectID == 0 && (req.TargetMobID != 0 || req.Damage != 0 || req.Defeated) || req.TargetObjectID > 0 && (req.TargetMobID == 0 || req.Damage == 0) {
		return domain.SkillAttackResult{}, domain.ErrInvalidInput
	}
	if req.TargetObjectID > 0 && (len(req.SpawnOperationID) < 16 || len(req.SpawnOperationID) > 200) || req.TargetObjectID == 0 && req.SpawnOperationID != "" {
		return domain.SkillAttackResult{}, domain.ErrInvalidInput
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SkillAttackResult{}, e
	}
	defer tx.Rollback()
	c, e := lockInventoryCharacter(ctx, tx, req.Session)
	if e != nil {
		return domain.SkillAttackResult{}, e
	}
	if e = rejectPartyCombatCompatibility(ctx, tx, c.ID); e != nil {
		return domain.SkillAttackResult{}, e
	}
	r := domain.SkillAttackResult{Character: c}
	rows, e := tx.QueryContext(ctx, `SELECT operation_id,spawn_operation_id,session_id,generation,client_tick,skill_id,target_object_id,target_mob_id,damage,defeated,experience FROM asset_skill_attacks WHERE character_id=$1 AND (operation_id=$2 OR (session_id=$3 AND generation=$4 AND client_tick=$5))`, c.ID, req.OperationID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick))
	if e != nil {
		return r, e
	}
	found := 0
	for rows.Next() {
		var old domain.SkillAttackRequest
		var sid string
		var generation, tick int64
		e = rows.Scan(&old.OperationID, &old.SpawnOperationID, &sid, &generation, &tick, &old.SkillID, &old.TargetObjectID, &old.TargetMobID, &old.Damage, &old.Defeated, &old.Experience)
		if e != nil {
			rows.Close()
			return r, e
		}
		found++
		if old.OperationID != req.OperationID || old.SpawnOperationID != req.SpawnOperationID || sid != req.Session.SessionID || generation != req.Session.Generation || tick != int64(req.ClientTick) || old.SkillID != req.SkillID || old.TargetObjectID != req.TargetObjectID || old.TargetMobID != req.TargetMobID || old.Damage != req.Damage || old.Defeated != req.Defeated || old.Experience != req.Experience {
			rows.Close()
			return r, domain.ErrForbidden
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return r, e
	}
	if found > 0 {
		if found != 1 {
			return r, domain.ErrForbidden
		}
		r.Replayed = true
		return r, nil
	}
	if e = carrySkillVitals(&c, req.Session); e != nil {
		return r, e
	}
	if c.HP <= 0 {
		return r, domain.ErrForbidden
	}
	var now time.Time
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	state := learned(c, req.SkillID)
	if state.Level < 1 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
		return r, domain.ErrForbidden
	}
	skill, e := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), req.SkillID)
	if e != nil || skill.Disabled {
		return r, domain.ErrInvalidInput
	}
	if !skill.CanLearnBy(c) {
		return r, domain.ErrForbidden
	}
	values, e := skill.Values(state.Level)
	if e != nil {
		return r, domain.ErrInvalidInput
	}
	if req.Damage > values["fixdamage"] || values["mpCon"] < 0 {
		return r, domain.ErrInvalidInput
	}
	resources, resourceErr := skills.ResolveResourceCost(c, req.SkillID, values["mpCon"], 0, now)
	if resourceErr != nil {
		return r, resourceErr
	}
	if c.MP < resources.MP {
		return r, domain.ErrInsufficient
	}
	if req.TargetMobID > 0 {
		catalog, e := content.DefaultCatalog()
		if e != nil {
			return r, e
		}
		mob, e := catalog.Mob(req.TargetMobID)
		if e != nil || req.Defeated && req.Experience != mob.EXP {
			return r, domain.ErrInvalidInput
		}
	}
	if req.Defeated {
		var awarded bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_combat_rewards WHERE operation_id=$1)`, req.SpawnOperationID).Scan(&awarded); e != nil {
			return r, e
		}
		if awarded {
			return r, domain.ErrAlreadyExists
		}
	}
	c.MP -= resources.MP
	if req.Defeated {
		if e = applyQuestExperience(&c, req.Experience, req.OperationID); e != nil {
			return r, e
		}
		if e = recordQuestKill(ctx, tx, &c, req.SpawnOperationID, req.TargetMobID); e != nil {
			return r, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,exp=$2,str=$3,ap=$4,sp=$5 WHERE id=$6`, c.Level, c.EXP, c.STR, c.AP, c.SP, c.ID); e != nil {
		return r, e
	}
	if e = commitInventoryState(ctx, tx, &c); e != nil {
		return r, e
	}
	if req.Defeated {
		if _, e = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,$3,$4)`, req.SpawnOperationID, c.ID, req.Experience, c.Revision); e != nil {
			return r, e
		}
		if e = enqueueCombatLootTx(ctx, tx, req.Session, c, req.SpawnOperationID, req.TargetMobID, req.TargetX, req.TargetY); e != nil {
			return r, e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO asset_skill_attacks(character_id,operation_id,spawn_operation_id,session_id,generation,client_tick,skill_id,target_object_id,target_mob_id,damage,defeated,experience) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, c.ID, req.OperationID, req.SpawnOperationID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick), req.SkillID, req.TargetObjectID, req.TargetMobID, req.Damage, req.Defeated, req.Experience); e != nil {
		return r, e
	}
	if e = tx.Commit(); e != nil {
		return domain.SkillAttackResult{}, e
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	r.Character = c
	return r, nil
}
