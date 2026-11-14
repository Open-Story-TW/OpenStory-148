package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func (s *Store) dropInventory(ctx context.Context, tx *sql.Tx, r domain.InventoryMoveRequest, result *domain.InventoryResult) error {
	c := &result.Character
	if r.Session.ChannelID < 1 || c.MapID != r.Session.Character.MapID || r.Session.Character.X < -32768 || r.Session.Character.X > 32767 || r.Session.Character.Y < -32768 || r.Session.Character.Y > 32767 {
		return domain.ErrInvalidInput
	}
	drop := domain.ItemDrop{WorldID: c.WorldID, ChannelID: r.Session.ChannelID, MapID: c.MapID, OwnerID: c.ID, X: r.Session.Character.X, Y: r.Session.Character.Y, InvType: r.InvType, Quantity: r.Quantity}
	var equipment []byte
	if r.InvType == 1 {
		index := -1
		for i, e := range c.Equipment {
			if e.Slot == r.Source {
				index = i
				break
			}
		}
		if index < 0 {
			return domain.ErrNotFound
		}
		e := c.Equipment[index]
		definition, definitionErr := inventoryDefinition(e.ItemID)
		if r.Quantity != 1 || definitionErr != nil || definition.InvType != 1 || !definition.Droppable() {
			return domain.ErrInvalidInput
		}
		drop.TemplateID = e.ItemID
		drop.Equipment = &e
		equipment, _ = json.Marshal(e)
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1`, e.ID); err != nil {
			return err
		}
		c.Equipment = append(c.Equipment[:index], c.Equipment[index+1:]...)
		result.Changes = []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: r.Source}}
	} else {
		index := -1
		for i, item := range c.Items {
			if item.InvType == r.InvType && item.Slot == r.Source {
				index = i
				break
			}
		}
		if index < 0 {
			return domain.ErrNotFound
		}
		item := c.Items[index]
		definition, definitionErr := inventoryDefinition(item.TemplateID)
		if definitionErr != nil || definition.InvType != r.InvType || !definition.Droppable() || r.Quantity > item.Quantity {
			return domain.ErrInvalidInput
		}
		drop.TemplateID = item.TemplateID
		if r.Quantity == item.Quantity {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
				return err
			}
			c.Items = append(c.Items[:index], c.Items[index+1:]...)
			result.Changes = []domain.InventoryChange{{Mode: 3, InvType: r.InvType, Slot: r.Source}}
		} else {
			c.Items[index].Quantity -= r.Quantity
			c.Items[index].Revision++
			if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, c.Items[index].Quantity, item.ID); err != nil {
				return err
			}
			result.Changes = []domain.InventoryChange{{Mode: 1, InvType: r.InvType, Slot: r.Source, Quantity: c.Items[index].Quantity}}
		}
	}
	// Keep the bounded native drop snapshot complete; reject new drops when full
	// rather than commit inventory that cannot be displayed on reconnect.
	room := fmt.Sprintf("item-room/%d/%d/%d", drop.WorldID, drop.ChannelID, drop.MapID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, room); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, drop.WorldID, drop.ChannelID, drop.MapID).Scan(&count); err != nil {
		return err
	}
	if count >= 512 {
		return domain.ErrInvalidInput
	}
	err := tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,equipment) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING object_id`, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.X, drop.Y, drop.InvType, drop.TemplateID, drop.Quantity, nullableJSON(equipment)).Scan(&drop.ObjectID)
	if err != nil {
		return err
	}
	result.Drop = &drop
	return nil
}

func nullableJSON(raw []byte) any {
	if raw == nil {
		return nil
	}
	return raw
}

func (s *Store) ListItemDrops(ctx context.Context, worldID, channelID, mapID int) ([]domain.ItemDrop, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT object_id,world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,mesos,equipment,player_drop,quest_id,quest_drop_type,quest_party_id,COALESCE((extract(epoch FROM quest_owner_until)*1000)::bigint,0) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp() ORDER BY object_id LIMIT 512`, worldID, channelID, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ItemDrop, 0)
	for rows.Next() {
		var drop domain.ItemDrop
		var raw []byte
		if err = rows.Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.OwnerID, &drop.X, &drop.Y, &drop.InvType, &drop.TemplateID, &drop.Quantity, &drop.Mesos, &raw, &drop.PlayerDrop, &drop.QuestID, &drop.QuestDropType, &drop.QuestPartyID, &drop.QuestOwnerUntil); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &drop.Equipment); err != nil {
				return nil, err
			}
		}
		items = append(items, drop)
	}
	return items, rows.Err()
}

func (s *Store) PickupItem(ctx context.Context, r domain.ItemPickupRequest) (domain.InventoryResult, error) {
	if r.ObjectID < 1000000000 {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	var drop domain.ItemDrop
	var equipment []byte
	var picked sql.NullInt64
	var expired bool
	var protected bool
	err = tx.QueryRowContext(ctx, `SELECT object_id,world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,mesos,equipment,picked_up_by,expired_at IS NOT NULL OR expires_at<=clock_timestamp(),player_drop,quest_id,quest_drop_type,quest_party_id,COALESCE((extract(epoch FROM quest_owner_until)*1000)::bigint,0),COALESCE(quest_owner_until>clock_timestamp(),false) FROM asset_item_drops WHERE object_id=$1 FOR UPDATE`, r.ObjectID).Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.OwnerID, &drop.X, &drop.Y, &drop.InvType, &drop.TemplateID, &drop.Quantity, &drop.Mesos, &equipment, &picked, &expired, &drop.PlayerDrop, &drop.QuestID, &drop.QuestDropType, &drop.QuestPartyID, &drop.QuestOwnerUntil, &protected)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InventoryResult{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.InventoryResult{}, err
	}
	if picked.Valid {
		if picked.Int64 != c.ID {
			return domain.InventoryResult{}, domain.ErrNotFound
		}
		return domain.InventoryResult{Character: c, Drop: &drop, Replayed: true}, nil
	}
	if expired {
		return domain.InventoryResult{}, domain.ErrNotFound
	}
	if c.Revision != r.Session.Character.Revision {
		return domain.InventoryResult{}, domain.ErrStaleOwner
	}
	if drop.WorldID != r.Session.Lease.WorldID || drop.ChannelID != r.Session.ChannelID || drop.MapID != r.Session.Character.MapID || r.Session.Character.HP <= 0 {
		return domain.InventoryResult{}, domain.ErrForbidden
	}
	dx, dy := int64(drop.X)-int64(r.Session.Character.X), int64(drop.Y)-int64(r.Session.Character.Y)
	if dx*dx+dy*dy > 180*180 {
		return domain.InventoryResult{}, domain.ErrForbidden
	}
	if drop.QuestID > 0 {
		remaining, needErr := content.QuestDropRemaining(os.Getenv("MAPLEGO_WZ_ROOT"), c, drop.QuestID, drop.TemplateID)
		if needErr != nil || remaining < drop.Quantity {
			return domain.InventoryResult{}, domain.ErrForbidden
		}
		if drop.OwnerID != c.ID && drop.QuestDropType < 2 && protected {
			if drop.QuestDropType != 1 {
				return domain.InventoryResult{}, domain.ErrForbidden
			}
			var sameParty bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_party_members a JOIN game_party_members b ON a.party_id=b.party_id JOIN game_parties p ON p.id=a.party_id WHERE a.character_id=$1 AND b.character_id=$2 AND p.world_id=$3)`, c.ID, drop.OwnerID, c.WorldID).Scan(&sameParty); err != nil {
				return domain.InventoryResult{}, err
			}
			if !sameParty {
				return domain.InventoryResult{}, domain.ErrForbidden
			}
		}
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	result := domain.InventoryResult{Character: c, Drop: &drop}
	if drop.Mesos > 0 {
		if drop.InvType != 0 || drop.TemplateID != 0 || drop.Mesos > domain.MaxMoney()-result.Character.Mesos {
			return domain.InventoryResult{}, domain.ErrInsufficient
		}
		result.Character.Mesos += drop.Mesos
	} else if drop.InvType == 1 {
		if err = json.Unmarshal(equipment, &drop.Equipment); err != nil || drop.Equipment == nil {
			return domain.InventoryResult{}, domain.ErrInvalidInput
		}
		slot := FreeEquipmentSlot(&c)
		if slot == 0 {
			return domain.InventoryResult{}, domain.ErrInsufficient
		}
		e := *drop.Equipment
		e.Slot = slot
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_equipment(id,character_id,item_id,slot,str,dex,int_stat,luk,hp,mp,watk,matk,wdef,mdef,acc,avoid,speed,jump,upgrade_slots) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, e.ID, c.ID, e.ItemID, e.Slot, e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots); err != nil {
			return domain.InventoryResult{}, err
		}
		result.Character.Equipment = append(c.Equipment, e)
		result.Changes = []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: slot, Equipment: &e}}
	} else {
		if err = pickupBundle(ctx, tx, drop, &result); err != nil {
			return domain.InventoryResult{}, err
		}
	}
	if err = commitInventoryState(ctx, tx, &result.Character); err != nil {
		return domain.InventoryResult{}, err
	}
	if drop.Mesos > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'ground_meso_pickup')`, fmt.Sprintf("ground-meso-pickup-%d", drop.ObjectID), c.ID, c.AccountID, c.WorldID, drop.Mesos, result.Character.Mesos); err != nil {
			return domain.InventoryResult{}, err
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_item_drops SET picked_up_by=$1,pickup_result=$2,picked_up_at=now() WHERE object_id=$3`, c.ID, raw, drop.ObjectID); err != nil {
		return domain.InventoryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.InventoryResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

func pickupBundle(ctx context.Context, tx *sql.Tx, drop domain.ItemDrop, result *domain.InventoryResult) error {
	definition, err := inventoryDefinition(drop.TemplateID)
	if err != nil || drop.InvType != definition.InvType || drop.InvType < 2 || drop.InvType > 4 {
		return domain.ErrInvalidInput
	}
	remaining := drop.Quantity
	for i, item := range result.Character.Items {
		if item.InvType != drop.InvType || item.TemplateID != drop.TemplateID || item.Quantity >= definition.SlotMax || item.CashID > 0 {
			continue
		}
		amount := min(remaining, definition.SlotMax-item.Quantity)
		item.Quantity += amount
		item.Revision++
		remaining -= amount
		if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, item.Quantity, item.ID); err != nil {
			return err
		}
		result.Character.Items[i] = item
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: drop.InvType, Slot: item.Slot, Quantity: item.Quantity})
		if remaining == 0 {
			return nil
		}
	}
	for slot := 1; slot <= result.Character.Capacity(drop.InvType) && remaining > 0; slot++ {
		free := true
		for _, item := range result.Character.Items {
			if item.InvType == drop.InvType && item.Slot == slot {
				free = false
				break
			}
		}
		if !free {
			continue
		}
		item := domain.InventoryItem{TemplateID: drop.TemplateID, InvType: drop.InvType, Slot: slot, Quantity: min(remaining, definition.SlotMax), Revision: 1}
		if err := tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id`, result.Character.ID, item.InvType, item.Slot, item.TemplateID, item.Quantity).Scan(&item.ID); err != nil {
			return err
		}
		remaining -= item.Quantity
		result.Character.Items = append(result.Character.Items, item)
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: item.InvType, Slot: slot, Item: &item})
	}
	if remaining > 0 {
		return domain.ErrInsufficient
	}
	return nil
}
