package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"maple-go-server/internal/domain"
)

var _ domain.SecondJobAdvancementRepository = (*Store)(nil)

func spearmanAdvancementStats(c domain.Character, op string) (domain.Character, error) {
	if c.Job != 100 || c.Level < 30 || c.Level > domain.MaxCharacterLevel() || c.VitalsModel != 1 || c.HP <= 0 || c.HP > c.MaxHP || c.MP < 0 || c.MP > c.MaxMP {
		return c, domain.ErrForbidden
	}
	if len(op) < 16 || len(op) > 128 || c.SP < 0 || c.SP >= 65535 {
		return c, domain.ErrInvalidInput
	}
	// MapleCharacter.changeJob(130): no beginner stat reset, regular SP+1,
	// intrinsic HP+300..350, no MP gain, then full effective vitals.
	hash := sha256.Sum256([]byte(op))
	gain := 300 + int(binary.LittleEndian.Uint64(hash[:8])%51)
	c.Job = 130
	c.SP++
	c.BaseMaxHP = min(domain.MaxVital(), c.BaseMaxHP+gain)
	if err := recomputeVitals(&c); err != nil {
		return c, err
	}
	c.HP, c.MP = c.MaxHP, c.MaxMP
	return c, nil
}
func (s *Store) AdvanceSecondJob(ctx context.Context, r domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	if r.NPCID != 1052001 || r.Script != "npc/1052001.lua" || r.TargetJob != 130 || len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return domain.JobAdvancementResult{}, domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	var prior []byte
	var source string
	var target int
	err = tx.QueryRowContext(ctx, `SELECT source_script,to_job,result FROM game_job_advancements WHERE character_id=$1 AND operation_id=$2`, value.ID, r.OperationID).Scan(&source, &target, &prior)
	if err == nil {
		if source != r.Script || target != r.TargetJob {
			return domain.JobAdvancementResult{}, domain.ErrForbidden
		}
		var result domain.JobAdvancementResult
		if err = json.Unmarshal(prior, &result); err != nil {
			return result, err
		}
		result.Character, result.Changes, result.Replayed = value, nil, true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.JobAdvancementResult{}, err
	}
	if value.MapID != 103000003 || r.Session.Character.MapID != 103000003 {
		return domain.JobAdvancementResult{}, domain.ErrForbidden
	}
	if err = carrySkillVitals(&value, r.Session); err != nil {
		return domain.JobAdvancementResult{}, err
	}
	value, err = spearmanAdvancementStats(value, r.OperationID)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET job=$1,sp=$2 WHERE id=$3`, value.Job, value.SP, value.ID); err != nil {
		return domain.JobAdvancementResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &value); err != nil {
		return domain.JobAdvancementResult{}, err
	}
	result := domain.JobAdvancementResult{InventoryResult: domain.InventoryResult{Character: value}, FromJob: 100, ToJob: 130}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_job_advancements(character_id,operation_id,source_script,from_job,to_job,result)VALUES($1,$2,$3,100,130,$4)`, value.ID, r.OperationID, r.Script, raw); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return result, nil
}

var _ domain.SecondJobOutcomeRepository = (*Store)(nil)

func (s *Store) ResolveSecondJob(ctx context.Context, r domain.JobAdvancementRequest) (domain.JobAdvancementResult, bool, error) {
	if r.NPCID != 1052001 || r.Script != "npc/1052001.lua" || r.TargetJob != 130 || len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return domain.JobAdvancementResult{}, false, domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.JobAdvancementResult{}, false, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.JobAdvancementResult{}, false, err
	}
	var raw []byte
	var source string
	var target int
	err = tx.QueryRowContext(ctx, `SELECT source_script,to_job,result FROM game_job_advancements WHERE character_id=$1 AND operation_id=$2`, value.ID, r.OperationID).Scan(&source, &target, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.JobAdvancementResult{}, false, nil
	}
	if err != nil {
		return domain.JobAdvancementResult{}, false, err
	}
	if source != r.Script || target != 130 {
		return domain.JobAdvancementResult{}, true, domain.ErrForbidden
	}
	var result domain.JobAdvancementResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, true, err
	}
	if result.Character.Revision != value.Revision || value.Job != 130 || result.FromJob != 100 || result.ToJob != 130 {
		return domain.JobAdvancementResult{}, true, domain.ErrStaleOwner
	}
	result.Character, result.Changes, result.Replayed = value, nil, true
	return result, true, nil
}
