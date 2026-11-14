package persistence

import (
	"context"
	"database/sql"
	"errors"
	"maple-go-server/internal/domain"
)

func (s *Store) SaveCharacter(ctx context.Context, session domain.CharacterSession, value domain.Character) error {
	if value.ID != session.CharacterID || value.AccountID != session.AccountID || value.WorldID != session.Lease.WorldID || value.Revision < 1 {
		return domain.ErrForbidden
	}
	if value.VitalsModel != session.Character.VitalsModel || value.BaseMaxHP != session.Character.BaseMaxHP || value.BaseMaxMP != session.Character.BaseMaxMP || value.VitalsOrigin != session.Character.VitalsOrigin {
		return domain.ErrForbidden
	}
	if value.VitalsModel == 1 {
		derived := value
		// Checkpoints never accept asset/skill/job changes. Derive the cap
		// from the authenticated live aggregate, not replacements in value.
		derived.Equipment, derived.Skills, derived.Job = session.Character.Equipment, session.Character.Skills, session.Character.Job
		if err := recomputeVitals(&derived); err != nil {
			return err
		}
		value.MaxHP, value.MaxMP = derived.MaxHP, derived.MaxMP
	}
	if value.Level < 1 || value.HP < 0 || value.MP < 0 || value.MaxHP < 1 || value.MaxMP < 0 || value.HP > value.MaxHP || value.MP > value.MaxMP || value.EXP < 0 || value.EXP > domain.MaxExperience() || value.Mesos != session.Character.Mesos {
		return domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, session.Lease); err != nil {
		return err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR SHARE`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_character_ownership WHERE character_id=$1 AND account_id=$2 AND session_id=$3 AND generation=$4 AND instance_id=$5 AND epoch=$6 FOR SHARE`, session.CharacterID, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,hp=$2,mp=$3,max_hp=$4,max_mp=$5,ap=$6,sp=$7,exp=$8,map_id=$9,spawn_point=$10,x=$11,y=$12,revision=revision+1 WHERE id=$13 AND account_id=$14 AND world_id=$15 AND revision=$16`, value.Level, value.HP, value.MP, value.MaxHP, value.MaxMP, value.AP, value.SP, value.EXP, value.MapID, value.SpawnPoint, value.X, value.Y, value.ID, value.AccountID, value.WorldID, value.Revision)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return domain.ErrStaleOwner
	}
	// Appearance/asset changes are not accepted through progress checkpoint.
	next := session.Character
	next.Level = value.Level
	next.HP = value.HP
	next.MP = value.MP
	next.MaxHP = value.MaxHP
	next.MaxMP = value.MaxMP
	next.AP = value.AP
	next.SP = value.SP
	next.EXP = value.EXP
	next.MapID = value.MapID
	next.SpawnPoint = value.SpawnPoint
	next.X = value.X
	next.Y = value.Y
	next.Revision = value.Revision + 1
	raw, err := marshalRoster(next)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1,revision=$2 WHERE character_id=$3`, raw, next.Revision, value.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type,account_id,world_id) VALUES('roster.invalidate',$1,$2)`, value.AccountID, value.WorldID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return nil
}
func (s *Store) ReleaseCharacter(ctx context.Context, session domain.CharacterSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Save and transfer use session -> ownership order. Lock the account row
	// first here too, even for a stale cleanup, to avoid an inverse-order cycle.
	var accountID int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, session.AccountID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM ops_character_ownership WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND instance_id=$4 AND epoch=$5`, session.CharacterID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline',expires_at=clock_timestamp() WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND owner_instance=$4 AND world_epoch=$5 AND state IN ('world','transfer')`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch); err != nil {
		return err
	}
	return tx.Commit()
}
