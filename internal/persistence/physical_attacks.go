package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

var _ domain.PhysicalAttackRepository = (*Store)(nil)

func physicalPayloadHash(req domain.PhysicalAttackRequest) (string, error) {
	raw, err := json.Marshal(struct {
		SkillID int
		Tick    uint32
		Targets []domain.PhysicalAttackTarget
	}{req.SkillID, req.ClientTick, req.Targets})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// ApplyPhysicalSkillAttack charges MP once and commits every validated kill,
// quest counter and durable loot job in one fenced transaction. No partial
// target deduction/reward can escape a later target validation failure.
func (s *Store) ApplyPhysicalSkillAttack(ctx context.Context, req domain.PhysicalAttackRequest) (domain.SkillAttackResult, error) {
	if req.SkillID != 1001004 && req.SkillID != 1001005 || len(req.OperationID) < 16 || len(req.OperationID) > 128 || len(req.Targets) > 6 {
		return domain.SkillAttackResult{}, domain.ErrInvalidInput
	}
	seen := map[int32]bool{}
	spawns := map[string]bool{}
	for _, target := range req.Targets {
		if target.ObjectID <= 0 || target.MobID < 1 || len(target.SpawnOperationID) < 16 || len(target.SpawnOperationID) > 200 || target.Damage < 0 || target.Experience < 0 || target.Experience > domain.MaxExperience() || !target.Defeated && target.Experience != 0 || target.Defeated && target.Damage == 0 || seen[target.ObjectID] || spawns[target.SpawnOperationID] {
			return domain.SkillAttackResult{}, domain.ErrInvalidInput
		}
		seen[target.ObjectID], spawns[target.SpawnOperationID] = true, true
	}
	hash, err := physicalPayloadHash(req)
	if err != nil {
		return domain.SkillAttackResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SkillAttackResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, req.Session)
	if err != nil {
		return domain.SkillAttackResult{}, err
	}
	if err = rejectPartyCombatCompatibility(ctx, tx, c.ID); err != nil {
		return domain.SkillAttackResult{}, err
	}
	r := domain.SkillAttackResult{Character: c}
	rows, err := tx.QueryContext(ctx, `SELECT operation_id,session_id,generation,client_tick,skill_id,payload_hash FROM asset_physical_attacks WHERE character_id=$1 AND (operation_id=$2 OR (session_id=$3 AND generation=$4 AND client_tick=$5))`, c.ID, req.OperationID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick))
	if err != nil {
		return r, err
	}
	found := 0
	for rows.Next() {
		var op, sid, priorHash string
		var gen, tick int64
		var skillID int
		if err = rows.Scan(&op, &sid, &gen, &tick, &skillID, &priorHash); err != nil {
			rows.Close()
			return r, err
		}
		found++
		if op != req.OperationID || sid != req.Session.SessionID || gen != req.Session.Generation || tick != int64(req.ClientTick) || skillID != req.SkillID || priorHash != hash {
			rows.Close()
			return r, domain.ErrForbidden
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	if found > 0 {
		if found != 1 {
			return r, domain.ErrForbidden
		}
		r.Replayed = true
		return r, nil
	}
	var otherAttack bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_skill_attacks WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4)`, c.ID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick)).Scan(&otherAttack); err != nil {
		return r, err
	}
	if otherAttack {
		return r, domain.ErrForbidden
	}
	if err = carrySkillVitals(&c, req.Session); err != nil {
		return r, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return r, err
	}
	profile, err := content.PhysicalAttackProfileFor(os.Getenv("MAPLEGO_WZ_ROOT"), c, req.SkillID, now)
	if err != nil {
		return r, domain.ErrForbidden
	}
	if len(req.Targets) > profile.MaxTargets {
		return r, domain.ErrInvalidInput
	}
	resources, resourceErr := skills.ResolveResourceCost(c, req.SkillID, profile.MP, 0, now)
	if resourceErr != nil {
		return r, resourceErr
	}
	if c.MP < resources.MP {
		return r, domain.ErrInsufficient
	}
	for _, cd := range c.SkillCooldowns {
		if cd.ID == req.SkillID && cd.ExpiresAt.After(now) {
			return r, domain.ErrAlreadyExists
		}
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return r, err
	}
	// Complete all authority/receipt checks before changing MP, EXP or quests.
	for _, target := range req.Targets {
		mob, e := catalog.Mob(target.MobID)
		if e != nil {
			return r, domain.ErrInvalidInput
		}
		maximum, e := profile.MaximumDamage(mob)
		if e != nil || target.Damage > maximum || target.Defeated && target.Experience != mob.EXP {
			return r, domain.ErrInvalidInput
		}
		if target.Defeated {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_combat_rewards WHERE operation_id=$1)`, target.SpawnOperationID).Scan(&exists); err != nil {
				return r, err
			}
			if exists {
				return r, domain.ErrAlreadyExists
			}
		}
	}
	c.MP -= resources.MP
	for _, target := range req.Targets {
		if !target.Defeated {
			continue
		}
		if err = applyQuestExperience(&c, target.Experience, target.SpawnOperationID); err != nil {
			return r, err
		}
		if err = recordQuestKill(ctx, tx, &c, target.SpawnOperationID, target.MobID); err != nil {
			return r, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,exp=$2,str=$3,ap=$4,sp=$5 WHERE id=$6`, c.Level, c.EXP, c.STR, c.AP, c.SP, c.ID); err != nil {
		return r, err
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return r, err
	}
	for _, target := range req.Targets {
		if !target.Defeated {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,$3,$4)`, target.SpawnOperationID, c.ID, target.Experience, c.Revision); err != nil {
			return r, err
		}
		if err = enqueueCombatLootTx(ctx, tx, req.Session, c, target.SpawnOperationID, target.MobID, target.X, target.Y); err != nil {
			return r, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_physical_attacks(character_id,operation_id,session_id,generation,client_tick,skill_id,payload_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, c.ID, req.OperationID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick), req.SkillID, hash); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return domain.SkillAttackResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	r.Character = c
	return r, nil
}
