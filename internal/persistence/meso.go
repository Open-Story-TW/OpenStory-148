package persistence

import (
	"context"
	"database/sql"
	"errors"
	"maple-go-server/internal/domain"
)

// maxMesos bounds one meso amount where the client still sends 32 bits (a
// price, a drop). Trade offers and storage transfers follow domain.MaxMoney.
// Balances are bounded by domain.MaxMoney instead.
const maxMesos int64 = 2147483647

// AdjustMesos persists a signed meso delta under world fencing and an idempotent operation_id.
// Progress checkpoints must not change mesos; economic commands use this path instead.
func (s *Store) AdjustMesos(ctx context.Context, adj domain.MesoAdjustment) (domain.Character, error) {
	session := adj.Session
	if adj.Delta == 0 || len(adj.OperationID) < 16 || adj.Reason == "" {
		return domain.Character{}, domain.ErrInvalidInput
	}
	if session.CharacterID <= 0 || session.AccountID <= 0 || session.Character.ID != session.CharacterID {
		return domain.Character{}, domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Character{}, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, session.Lease); err != nil {
		return domain.Character{}, err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR SHARE`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_character_ownership WHERE character_id=$1 AND account_id=$2 AND session_id=$3 AND generation=$4 AND instance_id=$5 AND epoch=$6 FOR SHARE`, session.CharacterID, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}

	var mesos, revision int64
	var accountID int64
	var worldID int
	err = tx.QueryRowContext(ctx, `SELECT account_id, world_id, mesos, revision FROM game_characters WHERE id=$1 FOR UPDATE`, session.CharacterID).Scan(&accountID, &worldID, &mesos, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Character{}, err
	}
	if accountID != session.AccountID || worldID != session.Lease.WorldID {
		return domain.Character{}, domain.ErrForbidden
	}

	var priorCharacter int64
	var priorDelta int64
	err = tx.QueryRowContext(ctx, `SELECT character_id, delta FROM asset_meso_ledger WHERE operation_id=$1`, adj.OperationID).Scan(&priorCharacter, &priorDelta)
	if err == nil {
		if priorCharacter != session.CharacterID || priorDelta != adj.Delta {
			return domain.Character{}, domain.ErrForbidden
		}
		return readCharacterSQL(ctx, tx, session.CharacterID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, err
	}

	next := mesos + adj.Delta
	if next < 0 {
		return domain.Character{}, domain.ErrInsufficient
	}
	if next > domain.MaxMoney() {
		return domain.Character{}, domain.ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_characters SET mesos=$1, revision=revision+1 WHERE id=$2 AND revision=$3 AND mesos=$4`, next, session.CharacterID, revision, mesos)
	if err != nil {
		return domain.Character{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id, character_id, account_id, world_id, delta, balance_after, reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, adj.OperationID, session.CharacterID, session.AccountID, session.Lease.WorldID, adj.Delta, next, adj.Reason); err != nil {
		return domain.Character{}, err
	}
	value, err := readCharacterSQL(ctx, tx, session.CharacterID)
	if err != nil {
		return domain.Character{}, err
	}
	raw, err := marshalRoster(value)
	if err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1, revision=$2 WHERE character_id=$3`, raw, value.Revision, value.ID); err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type, account_id, world_id) VALUES('roster.invalidate',$1,$2)`, value.AccountID, value.WorldID); err != nil {
		return domain.Character{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Character{}, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return value, nil
}

type sqlQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readCharacterSQL(ctx context.Context, q sqlQuerier, id int64) (domain.Character, error) {
	var c domain.Character
	err := q.QueryRowContext(ctx, `SELECT id, account_id, world_id, name, gender, skin, face, hair, level, job, str, dex, int_stat, luk, hp, mp, max_hp, max_mp, ap, sp, exp, fame, mesos, map_id, spawn_point, x, y, gm, revision FROM game_characters WHERE id=$1`, id).Scan(
		&c.ID, &c.AccountID, &c.WorldID, &c.Name, &c.Gender, &c.Skin, &c.Face, &c.Hair, &c.Level, &c.Job, &c.STR, &c.DEX, &c.INT, &c.LUK, &c.HP, &c.MP, &c.MaxHP, &c.MaxMP, &c.AP, &c.SP, &c.EXP, &c.Fame, &c.Mesos, &c.MapID, &c.SpawnPoint, &c.X, &c.Y, &c.GM, &c.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Character{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id, item_id, slot, str, dex, int_stat, luk, hp, mp, watk, matk, wdef, mdef, acc, avoid, speed, jump, upgrade_slots FROM asset_equipment WHERE character_id=$1 ORDER BY slot`, id)
	if err != nil {
		return domain.Character{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e domain.Equipment
		if err = rows.Scan(&e.ID, &e.ItemID, &e.Slot, &e.STR, &e.DEX, &e.INT, &e.LUK, &e.HP, &e.MP, &e.WATK, &e.MATK, &e.WDEF, &e.MDEF, &e.ACC, &e.AVOID, &e.Speed, &e.Jump, &e.UpgradeSlots); err != nil {
			return domain.Character{}, err
		}
		c.Equipment = append(c.Equipment, e)
	}
	if err = rows.Err(); err != nil {
		return domain.Character{}, err
	}
	itemRows, err := q.QueryContext(ctx, `SELECT id, template_id, inv_type, slot, quantity, revision, expires_at FROM asset_items WHERE character_id=$1 ORDER BY inv_type, slot`, id)
	if err != nil {
		return domain.Character{}, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var item domain.InventoryItem
		if err = itemRows.Scan(&item.ID, &item.TemplateID, &item.InvType, &item.Slot, &item.Quantity, &item.Revision, &item.ExpiresAt); err != nil {
			return domain.Character{}, err
		}
		c.Items = append(c.Items, item)
	}
	if err = itemRows.Err(); err != nil {
		return domain.Character{}, err
	}
	if err = applyCashMetadata(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	c.Quests, err = readQuestStates(ctx, q, id)
	if err != nil {
		return domain.Character{}, err
	}
	c.QuestRecords, err = readQuestRecords(ctx, q, id)
	if err != nil {
		return domain.Character{}, err
	}
	if err = readSkillState(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	if err = readInventorySlots(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	if err = readIntrinsicPassives(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	if err = readBaseVitals(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	if err = readJobState(ctx, q, &c); err != nil {
		return domain.Character{}, err
	}
	return c, nil
}
