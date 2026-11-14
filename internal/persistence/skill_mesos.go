package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.SkillMesoRepository = (*Store)(nil)

func validSkillMesoOp(op string) bool { return len(op) >= 16 && len(op) <= 200 }

func lockItemRoom(ctx context.Context, tx *sql.Tx, world, channel, mapID int) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("item-room/%d/%d/%d", world, channel, mapID))
	return err
}

// ConsumeMesoDrops retires live meso drops in the actor's room in one
// transaction under the same room lock as drops and pickups. A replay returns
// the recorded drops; any missing or already-taken drop rejects the whole set.
func (s *Store) ConsumeMesoDrops(ctx context.Context, q domain.MesoConsumeRequest) ([]domain.ItemDrop, error) {
	ids := append([]int32(nil), q.ObjectIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if !validSkillMesoOp(q.OperationID) || q.SkillID <= 0 || len(ids) == 0 || len(ids) > 32 || q.Session.ChannelID < 1 || q.Session.ChannelID > 40 {
		return nil, domain.ErrInvalidInput
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return nil, domain.ErrInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, q.Session.Lease); err != nil {
		return nil, err
	}
	c, err := lockInventoryCharacter(ctx, tx, q.Session)
	if err != nil {
		return nil, err
	}
	if c.HP <= 0 || c.MapID != q.Session.Character.MapID {
		return nil, domain.ErrForbidden
	}
	if err = lockItemRoom(ctx, tx, c.WorldID, q.Session.ChannelID, c.MapID); err != nil {
		return nil, err
	}
	var owner int64
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT character_id,object_ids FROM asset_meso_consumptions WHERE operation_id=$1`, q.OperationID).Scan(&owner, &prior)
	if err == nil {
		var got []int32
		if owner != c.ID || json.Unmarshal(prior, &got) != nil || len(got) != len(ids) {
			return nil, domain.ErrForbidden
		}
		for i := range got {
			if got[i] != ids[i] {
				return nil, domain.ErrForbidden
			}
		}
		return consumedMesoDrops(ctx, tx, ids)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out, err := consumeMesoDropsTx(ctx, tx, c, q.Session.ChannelID, q.OperationID, q.SkillID, ids)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// consumeMesoDropsTx retires live mesos of c's map inside tx (the caller holds
// the character and item-room locks) and records the consumption.
func consumeMesoDropsTx(ctx context.Context, tx *sql.Tx, c domain.Character, channel int, op string, skillID int, ids []int32) ([]domain.ItemDrop, error) {
	want, _ := json.Marshal(ids)
	var err error
	var out []domain.ItemDrop
	var total int64
	for _, id := range ids {
		d := domain.ItemDrop{ObjectID: id, Quantity: 1}
		err = tx.QueryRowContext(ctx, `UPDATE asset_item_drops SET expired_at=clock_timestamp() WHERE object_id=$1 AND world_id=$2 AND channel_id=$3 AND map_id=$4 AND mesos>0 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp() RETURNING world_id,channel_id,map_id,owner_id,x,y,mesos`, id, c.WorldID, channel, c.MapID).Scan(&d.WorldID, &d.ChannelID, &d.MapID, &d.OwnerID, &d.X, &d.Y, &d.Mesos)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		total += d.Mesos
		out = append(out, d)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_meso_consumptions(operation_id,character_id,world_id,skill_id,object_ids,total) VALUES($1,$2,$3,$4,$5,$6)`, op, c.ID, c.WorldID, skillID, want, total); err != nil {
		return nil, err
	}
	return out, nil
}

// PeekMesoDrops reads the named live meso drops of the caller's map; the
// Meso Explosion settlement consumes them later in its own transaction.
func (s *Store) PeekMesoDrops(ctx context.Context, q domain.MesoConsumeRequest) ([]domain.ItemDrop, error) {
	if len(q.ObjectIDs) == 0 || len(q.ObjectIDs) > 32 || q.Session.ChannelID < 1 || q.Session.ChannelID > 40 {
		return nil, domain.ErrInvalidInput
	}
	c := q.Session.Character
	var out []domain.ItemDrop
	for _, id := range q.ObjectIDs {
		d := domain.ItemDrop{ObjectID: id, Quantity: 1}
		err := s.db.QueryRowContext(ctx, `SELECT world_id,channel_id,map_id,owner_id,x,y,mesos FROM asset_item_drops WHERE object_id=$1 AND world_id=$2 AND channel_id=$3 AND map_id=$4 AND mesos>0 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, id, c.WorldID, q.Session.ChannelID, c.MapID).Scan(&d.WorldID, &d.ChannelID, &d.MapID, &d.OwnerID, &d.X, &d.Y, &d.Mesos)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func consumedMesoDrops(ctx context.Context, tx *sql.Tx, ids []int32) ([]domain.ItemDrop, error) {
	var out []domain.ItemDrop
	for _, id := range ids {
		d := domain.ItemDrop{ObjectID: id, Quantity: 1}
		if err := tx.QueryRowContext(ctx, `SELECT world_id,channel_id,map_id,owner_id,x,y,mesos FROM asset_item_drops WHERE object_id=$1`, id).Scan(&d.WorldID, &d.ChannelID, &d.MapID, &d.OwnerID, &d.X, &d.Y, &d.Mesos); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// SpawnSkillMesos inserts a skill-created meso drop owned by the actor.
func (s *Store) SpawnSkillMesos(ctx context.Context, q domain.SkillMesoSpawnRequest) (domain.ItemDrop, error) {
	if !validSkillMesoOp(q.OperationID) || q.SkillID <= 0 || q.Amount < 1 || q.Amount > 50000 || q.Session.ChannelID < 1 || q.Session.ChannelID > 40 || q.X < -32768 || q.X > 32767 || q.Y < -32768 || q.Y > 32767 {
		return domain.ItemDrop{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ItemDrop{}, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, q.Session.Lease); err != nil {
		return domain.ItemDrop{}, err
	}
	c, err := lockInventoryCharacter(ctx, tx, q.Session)
	if err != nil {
		return domain.ItemDrop{}, err
	}
	drop := domain.ItemDrop{WorldID: c.WorldID, ChannelID: q.Session.ChannelID, MapID: c.MapID, OwnerID: c.ID, X: q.X, Y: q.Y, Quantity: 1, Mesos: q.Amount}
	err = tx.QueryRowContext(ctx, `SELECT object_id,world_id,channel_id,map_id,owner_id,x,y,mesos FROM asset_item_drops WHERE skill_operation_id=$1`, q.OperationID).Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.OwnerID, &drop.X, &drop.Y, &drop.Mesos)
	if err == nil {
		if drop.OwnerID != c.ID || drop.Mesos != q.Amount {
			return domain.ItemDrop{}, domain.ErrForbidden
		}
		return drop, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ItemDrop{}, err
	}
	if c.HP <= 0 || c.MapID != q.Session.Character.MapID {
		return domain.ItemDrop{}, domain.ErrForbidden
	}
	if err = lockItemRoom(ctx, tx, c.WorldID, q.Session.ChannelID, c.MapID); err != nil {
		return domain.ItemDrop{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, c.WorldID, q.Session.ChannelID, c.MapID).Scan(&count); err != nil {
		return domain.ItemDrop{}, err
	}
	if count >= 512 {
		return domain.ItemDrop{}, domain.ErrInsufficient
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,mesos,player_drop,skill_operation_id) VALUES($1,$2,$3,$4,$5,$6,0,0,1,$7,false,$8) RETURNING object_id`, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.X, drop.Y, drop.Mesos, q.OperationID).Scan(&drop.ObjectID); err != nil {
		return domain.ItemDrop{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ItemDrop{}, err
	}
	return drop, nil
}

// StealMobItem picks one ordinary (non-equipment, non-quest, non-cash) item
// from the monster's drop table with a roll pinned to the operation and
// drops it owned by the thief. A spawn is stolen from at most once.
func (s *Store) StealMobItem(ctx context.Context, q domain.MobStealRequest) (*domain.ItemDrop, error) {
	if !validSkillMesoOp(q.OperationID) || !validSkillMesoOp(q.SpawnOperationID) || q.SkillID <= 0 || q.MobID <= 0 || q.Session.ChannelID < 1 || q.Session.ChannelID > 40 || q.X < -32768 || q.X > 32767 || q.Y < -32768 || q.Y > 32767 {
		return nil, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, q.Session.Lease); err != nil {
		return nil, err
	}
	c, err := lockInventoryCharacter(ctx, tx, q.Session)
	if err != nil {
		return nil, err
	}
	var priorOp string
	var priorObject sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT operation_id,drop_object_id FROM asset_mob_steals WHERE spawn_operation_id=$1`, q.SpawnOperationID).Scan(&priorOp, &priorObject)
	if err == nil {
		if priorOp != q.OperationID {
			return nil, domain.ErrAlreadyExists
		}
		if !priorObject.Valid {
			return nil, nil
		}
		d := domain.ItemDrop{ObjectID: int32(priorObject.Int64)}
		if err = tx.QueryRowContext(ctx, `SELECT world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity FROM asset_item_drops WHERE object_id=$1`, d.ObjectID).Scan(&d.WorldID, &d.ChannelID, &d.MapID, &d.OwnerID, &d.X, &d.Y, &d.InvType, &d.TemplateID, &d.Quantity); err != nil {
			return nil, err
		}
		return &d, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if c.HP <= 0 || c.MapID != q.Session.Character.MapID {
		return nil, domain.ErrForbidden
	}
	if q.Boss {
		return nil, domain.ErrForbidden // Java handleSteal: bosses cannot be stolen from.
	}
	rows, err := content.MonsterDrops(q.MobID, c.MapID)
	if err != nil {
		return nil, err
	}
	var candidates []content.DropDefinition
	for _, row := range rows {
		if row.TemplateID <= 0 || row.Chance <= 0 || row.QuestID > 0 || row.Global {
			continue
		}
		def, e := inventoryDefinition(row.TemplateID)
		if e != nil || def.InvType < 2 || def.InvType > 4 || def.Cash || def.Quest {
			continue
		}
		candidates = append(candidates, row)
	}
	var drop *domain.ItemDrop
	template := 0
	if len(candidates) > 0 {
		row := candidates[lootRoll(q.OperationID, 0, 0, uint64(len(candidates)))]
		def, _ := inventoryDefinition(row.TemplateID)
		if err = lockItemRoom(ctx, tx, c.WorldID, q.Session.ChannelID, c.MapID); err != nil {
			return nil, err
		}
		d := domain.ItemDrop{WorldID: c.WorldID, ChannelID: q.Session.ChannelID, MapID: c.MapID, OwnerID: c.ID, X: q.X, Y: q.Y, InvType: def.InvType, TemplateID: row.TemplateID, Quantity: 1}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,skill_operation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,1,$9) RETURNING object_id`, d.WorldID, d.ChannelID, d.MapID, d.OwnerID, d.X, d.Y, d.InvType, d.TemplateID, q.OperationID).Scan(&d.ObjectID); err != nil {
			return nil, err
		}
		drop, template = &d, row.TemplateID
	}
	var object any
	if drop != nil {
		object = drop.ObjectID
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_mob_steals(spawn_operation_id,operation_id,character_id,world_id,skill_id,mob_id,template_id,drop_object_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, q.SpawnOperationID, q.OperationID, c.ID, c.WorldID, q.SkillID, q.MobID, template, object); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return drop, nil
}
