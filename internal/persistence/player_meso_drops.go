package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"maple-go-server/internal/domain"
)

var _ domain.MesoDropRepository = (*Store)(nil)

func (s *Store) DropMesosOnMap(ctx context.Context, request domain.MesoDropRequest) (domain.InventoryResult, error) {
	if request.Amount < 10 || request.Amount > 50000 || len(request.OperationID) < 16 || len(request.OperationID) > 128 || request.Session.ChannelID < 1 || request.Session.ChannelID > 40 || request.Session.Character.X < -32768 || request.Session.Character.X > 32767 || request.Session.Character.Y < -32768 || request.Session.Character.Y > 32767 {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, request.Session)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	var drop domain.ItemDrop
	err = tx.QueryRowContext(ctx, `SELECT object_id,world_id,channel_id,map_id,owner_id,x,y,mesos,player_drop FROM asset_item_drops WHERE source_operation_id=$1`, request.OperationID).Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.OwnerID, &drop.X, &drop.Y, &drop.Mesos, &drop.PlayerDrop)
	if err == nil {
		if drop.OwnerID != c.ID || drop.Mesos != request.Amount || drop.WorldID != c.WorldID {
			return domain.InventoryResult{}, domain.ErrForbidden
		}
		drop.Quantity = 1
		return domain.InventoryResult{Character: c, Drop: &drop, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.InventoryResult{}, err
	}
	if err = carrySkillVitals(&c, request.Session); err != nil {
		return domain.InventoryResult{}, err
	}
	if c.HP <= 0 || c.MapID != request.Session.Character.MapID {
		return domain.InventoryResult{}, domain.ErrForbidden
	}
	if c.Mesos < request.Amount {
		return domain.InventoryResult{}, domain.ErrInsufficient
	}
	room := fmt.Sprintf("item-room/%d/%d/%d", c.WorldID, request.Session.ChannelID, c.MapID)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, room); err != nil {
		return domain.InventoryResult{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, c.WorldID, request.Session.ChannelID, c.MapID).Scan(&count); err != nil {
		return domain.InventoryResult{}, err
	}
	if count >= 512 {
		return domain.InventoryResult{}, domain.ErrInsufficient
	}
	c.Mesos -= request.Amount
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return domain.InventoryResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'player_drop')`, request.OperationID, c.ID, c.AccountID, c.WorldID, -request.Amount, c.Mesos); err != nil {
		return domain.InventoryResult{}, err
	}
	drop = domain.ItemDrop{WorldID: c.WorldID, ChannelID: request.Session.ChannelID, MapID: c.MapID, OwnerID: c.ID, X: request.Session.Character.X, Y: request.Session.Character.Y, Quantity: 1, Mesos: request.Amount, PlayerDrop: true}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,mesos,player_drop,source_operation_id) VALUES($1,$2,$3,$4,$5,$6,0,0,1,$7,true,$8) RETURNING object_id`, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.X, drop.Y, drop.Mesos, request.OperationID).Scan(&drop.ObjectID); err != nil {
		return domain.InventoryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.InventoryResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return domain.InventoryResult{Character: c, Drop: &drop}, nil
}
