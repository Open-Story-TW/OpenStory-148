package persistence

import (
	"context"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// CompleteHorntailLoot seals the loot from the linked WZ body after the last
// visible part falls. A settled part is the durable receipt for the award.
func (s *Store) CompleteHorntailLoot(ctx context.Context, session domain.CharacterSession, receipt string, x, y int) (string, error) {
	mapID := session.Character.MapID
	first, mob, ok := content.HorntailLoot(mapID)
	if !ok || len(receipt) < 16 || len(receipt) > 170 {
		return "", domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return "", err
	}
	var proven bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM asset_combat_rewards r JOIN asset_combat_loot_jobs j USING(operation_id)
		WHERE r.operation_id=$1 AND r.character_id=$2 AND j.map_id=$3 AND j.world_id=$4
		  AND j.channel_id=$5 AND j.mob_id BETWEEN $6 AND $6+7)`,
		receipt, session.CharacterID, mapID, session.Lease.WorldID, session.ChannelID, first).Scan(&proven)
	if err != nil {
		return "", err
	}
	if !proven {
		return "", domain.ErrForbidden
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM game_characters
		WHERE id=$1 AND account_id=$2 AND world_id=$3 AND map_id=$4 FOR UPDATE`,
		session.CharacterID, session.AccountID, session.Lease.WorldID, mapID).Scan(&revision)
	if err != nil {
		return "", err
	}
	value, err := readCharacterSQL(ctx, tx, session.CharacterID)
	if err != nil {
		return "", err
	}
	op := receipt + "/horntail-reward"
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision)
		VALUES($1,$2,0,$3) ON CONFLICT DO NOTHING`, op, session.CharacterID, revision); err != nil {
		return "", err
	}
	if err := enqueueCombatLootTx(ctx, tx, session, value, op, mob, x, y); err != nil {
		return "", err
	}
	return op, tx.Commit()
}
