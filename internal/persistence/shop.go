package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.ShopRepository = (*Store)(nil)

func shopHash(r domain.ShopRequest) string {
	value := struct {
		AccountID, CharacterID              int64
		Session                             string
		Generation                          int64
		ShopID                              int
		Kind                                string
		EntryID, TemplateID, Slot, Quantity int
		DisplayedUnitPrice                  *int64 `json:",omitempty"`
	}{r.Session.AccountID, r.Session.CharacterID, r.Session.SessionID, r.Session.Generation, r.ShopID, r.Kind, r.EntryID, r.TemplateID, r.Slot, r.Quantity, r.DisplayedUnitPrice}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Store) ChangeShop(ctx context.Context, r domain.ShopRequest) (domain.ShopResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 || r.Quantity < 1 || r.Quantity > 30000 {
		return domain.ShopResult{}, domain.ErrInvalidInput
	}
	if r.Kind != "buy" && r.DisplayedUnitPrice != nil {
		return domain.ShopResult{}, domain.ErrInvalidInput
	}
	shop, err := content.NPCShop(r.ShopID)
	if err != nil {
		return domain.ShopResult{}, domain.ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ShopResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.ShopResult{}, err
	}
	var hash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_shop_operations WHERE operation_id=$1`, r.OperationID).Scan(&hash, &raw)
	if err == nil {
		if hash != shopHash(r) {
			return domain.ShopResult{}, domain.ErrForbidden
		}
		var result domain.ShopResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return domain.ShopResult{}, err
		}
		result.Inventory.Character = c
		result.Inventory.Changes = nil
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ShopResult{}, err
	}
	if c.Revision != r.Session.Character.Revision {
		return domain.ShopResult{}, domain.ErrStaleOwner
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	result := domain.ShopResult{Inventory: domain.InventoryResult{Character: c}}
	switch r.Kind {
	case "buy":
		err = buyShopItem(ctx, tx, shop, r, &result.Inventory)
	case "sell":
		err = sellShopItem(ctx, tx, r, &result.Inventory)
	case "recharge":
		err = rechargeShopItem(ctx, tx, r, &result.Inventory)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return domain.ShopResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
		return domain.ShopResult{}, err
	}
	if err = appendEconomicLedger(ctx, tx, "shop/"+r.OperationID, c, result.Inventory.Character, "npc_shop_"+r.Kind); err != nil {
		return domain.ShopResult{}, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return domain.ShopResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_shop_operations(operation_id,account_id,world_id,character_id,shop_id,request_hash,result) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.OperationID, c.AccountID, c.WorldID, c.ID, r.ShopID, shopHash(r), raw); err != nil {
		return domain.ShopResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ShopResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

func buyShopItem(ctx context.Context, tx *sql.Tx, shop domain.Shop, r domain.ShopRequest, result *domain.InventoryResult) error {
	var entry *domain.ShopItem
	for i := range shop.Items {
		if shop.Items[i].ID == r.EntryID && shop.Items[i].TemplateID == r.TemplateID {
			entry = &shop.Items[i]
			break
		}
	}
	if entry == nil {
		return domain.ErrForbidden
	}
	// The emitted catalog currently advertises discount=0, so the original UI
	// sends this exact unit price. Never use the assertion to calculate cost.
	if r.DisplayedUnitPrice != nil && (*r.DisplayedUnitPrice < 0 || *r.DisplayedUnitPrice != entry.Price) {
		return domain.ErrForbidden
	}
	if entry.MinLevel > result.Character.Level || entry.Period != 0 {
		return domain.ErrForbidden
	}
	definition, err := inventoryDefinition(entry.TemplateID)
	if err != nil || definition.Cash || definition.Quest {
		return domain.ErrInvalidInput
	}
	if len(shop.Ranks) > 0 && entry.PointType >= 0 {
		hasRank := false
		for rank, item := range shop.Ranks {
			if rank <= entry.PointType {
				for _, e := range result.Character.Equipment {
					if e.ItemID == item.ItemID {
						hasRank = true
					}
				}
				for _, e := range result.Character.Items {
					if e.TemplateID == item.ItemID {
						hasRank = true
					}
				}
			}
		}
		if !hasRank {
			return domain.ErrForbidden
		}
	}
	if entry.RequiredItem > 0 {
		if r.Quantity != 1 || entry.RequiredQuantity < 1 {
			return domain.ErrInvalidInput
		}
		remaining := entry.RequiredQuantity
		items := make([]domain.TradeItem, 0)
		for _, item := range result.Character.Items {
			if item.TemplateID == entry.RequiredItem && remaining > 0 {
				quantity := min(remaining, item.Quantity)
				items = append(items, domain.TradeItem{InvType: item.InvType, Slot: item.Slot, Quantity: quantity, InstanceID: item.ID, TradeSlot: 1})
				remaining -= quantity
			}
		}
		if remaining != 0 {
			return domain.ErrInsufficient
		}
		if _, err = takeInventoryItems(ctx, tx, result, items, false); err != nil {
			return err
		}
	} else {
		if entry.Price < 1 || entry.Price > maxMesos {
			return domain.ErrInvalidInput
		}
		cost := entry.Price * int64(r.Quantity)
		if cost > result.Character.Mesos {
			return domain.ErrInsufficient
		}
		result.Character.Mesos -= cost
	}
	if definition.InvType == 1 {
		for range r.Quantity {
			e := definition.Equipment
			e.Slot = FreeEquipmentSlot(&result.Character)
			if e.Slot == 0 {
				return domain.ErrInsufficient
			}
			if err = insertEquipment(ctx, tx, result.Character.ID, &e); err != nil {
				return err
			}
			result.Character.Equipment = append(result.Character.Equipment, e)
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: 1, Slot: e.Slot, Equipment: &e})
		}
		return nil
	}
	return pickupBundle(ctx, tx, domain.ItemDrop{InvType: definition.InvType, TemplateID: entry.TemplateID, Quantity: r.Quantity}, result)
}

func insertEquipment(ctx context.Context, tx *sql.Tx, characterID int64, e *domain.Equipment) error {
	return tx.QueryRowContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot,str,dex,int_stat,luk,hp,mp,watk,matk,wdef,mdef,acc,avoid,speed,jump,upgrade_slots) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id`, characterID, e.ItemID, e.Slot, e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots).Scan(&e.ID)
}

func sellShopItem(ctx context.Context, tx *sql.Tx, r domain.ShopRequest, result *domain.InventoryResult) error {
	definition, err := inventoryDefinition(r.TemplateID)
	if err != nil || definition.Price < 0 || definition.Cash || definition.Quest {
		return domain.ErrInvalidInput
	}
	if r.Slot < 1 || r.Slot > result.Character.Capacity(definition.InvType) {
		return domain.ErrInvalidInput
	}
	item := domain.TradeItem{InvType: definition.InvType, Slot: r.Slot, Quantity: r.Quantity, TradeSlot: 1}
	if item.InvType == 1 {
		for _, e := range result.Character.Equipment {
			if e.Slot == r.Slot && e.ItemID == r.TemplateID {
				item.InstanceID = e.ID
			}
		}
	} else {
		for _, e := range result.Character.Items {
			if e.Slot == r.Slot && e.TemplateID == r.TemplateID {
				item.InstanceID = e.ID
				if r.TemplateID/10000 == 207 || r.TemplateID/10000 == 233 {
					item.Quantity = e.Quantity
					r.Quantity = e.Quantity
				}
			}
		}
	}
	if item.InstanceID < 1 {
		return domain.ErrNotFound
	}
	if _, err = takeInventoryItems(ctx, tx, result, []domain.TradeItem{item}, false); err != nil {
		return err
	}
	unitPrice := definition.UnitPrice
	if r.TemplateID/10000 == 207 || r.TemplateID/10000 == 233 {
		unitPrice = float64(definition.Price) / float64(definition.SlotMax)
	}
	proceeds := int64(math.Ceil(unitPrice * float64(r.Quantity)))
	if proceeds > domain.MaxMoney()-result.Character.Mesos {
		return domain.ErrInvalidInput
	}
	result.Character.Mesos += proceeds
	return nil
}

func rechargeShopItem(ctx context.Context, tx *sql.Tx, r domain.ShopRequest, result *domain.InventoryResult) error {
	index := -1
	for i, item := range result.Character.Items {
		if item.InvType == 2 && item.Slot == r.Slot {
			index = i
			break
		}
	}
	if index < 0 {
		return domain.ErrNotFound
	}
	item := result.Character.Items[index]
	category := item.TemplateID / 10000
	if category != 207 && category != 233 {
		return domain.ErrInvalidInput
	}
	definition, err := inventoryDefinition(item.TemplateID)
	if err != nil {
		return err
	}
	missing := definition.SlotMax - item.Quantity
	if missing <= 0 {
		return nil
	}
	price := int64(math.Round(definition.UnitPrice * float64(missing)))
	if price < 0 || price > result.Character.Mesos {
		return domain.ErrInsufficient
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, definition.SlotMax, item.ID); err != nil {
		return err
	}
	result.Character.Items[index].Quantity = definition.SlotMax
	result.Character.Items[index].Revision++
	result.Character.Mesos -= price
	result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: 2, Slot: item.Slot, Quantity: definition.SlotMax})
	return nil
}
