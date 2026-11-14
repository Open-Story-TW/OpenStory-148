package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"maple-go-server/internal/domain"
)

var _ domain.TradeRepository = (*Store)(nil)
var _ domain.TradeReceiptRepository = (*Store)(nil)

func tradeHash(request domain.TradeSettlement) string {
	type identity struct {
		CharacterID, AccountID int64
		SessionID              string
		Generation             int64
	}
	value := struct {
		First, Second           identity
		FirstOffer, SecondOffer domain.TradeOffer
	}{identity{request.First.CharacterID, request.First.AccountID, request.First.SessionID, request.First.Generation}, identity{request.Second.CharacterID, request.Second.AccountID, request.Second.SessionID, request.Second.Generation}, request.FirstOffer, request.SecondOffer}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SettleTrade moves both sides under one SQL commit. Offers are not debited
// before both confirmations, so cancellation or process loss requires no refund.
func (s *Store) SettleTrade(ctx context.Context, r domain.TradeSettlement) (domain.TradeResult, error) {
	if len(r.ID) < 16 || len(r.ID) > 128 || r.First.CharacterID == r.Second.CharacterID || r.First.AccountID == r.Second.AccountID || r.First.Lease.WorldID != r.Second.Lease.WorldID || r.First.ChannelID != r.Second.ChannelID || r.First.Character.MapID != r.Second.Character.MapID || r.First.Character.HP <= 0 || r.Second.Character.HP <= 0 {
		return domain.TradeResult{}, domain.ErrInvalidInput
	}
	if r.FirstOffer.Mesos < 0 || r.SecondOffer.Mesos < 0 || r.FirstOffer.Mesos > domain.MaxMoney() || r.SecondOffer.Mesos > domain.MaxMoney() || len(r.FirstOffer.Items) > 9 || len(r.SecondOffer.Items) > 9 {
		return domain.TradeResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TradeResult{}, err
	}
	defer tx.Rollback()
	var first, second domain.Character
	// Stable order even when two callers reverse the participants.
	if r.First.CharacterID < r.Second.CharacterID {
		first, err = lockInventoryCharacter(ctx, tx, r.First)
		if err == nil {
			second, err = lockInventoryCharacter(ctx, tx, r.Second)
		}
	} else {
		second, err = lockInventoryCharacter(ctx, tx, r.Second)
		if err == nil {
			first, err = lockInventoryCharacter(ctx, tx, r.First)
		}
	}
	if err != nil {
		return domain.TradeResult{}, err
	}
	var priorHash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_trade_settlements WHERE trade_id=$1`, r.ID).Scan(&priorHash, &raw)
	if err == nil {
		if priorHash != tradeHash(r) {
			return domain.TradeResult{}, domain.ErrForbidden
		}
		var result domain.TradeResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return domain.TradeResult{}, err
		}
		result.First.Character = first
		result.Second.Character = second
		result.First.Changes = nil
		result.Second.Changes = nil
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.TradeResult{}, err
	}
	if first.Revision != r.First.Character.Revision || second.Revision != r.Second.Character.Revision {
		return domain.TradeResult{}, domain.ErrStaleOwner
	}
	if first.Mesos < r.FirstOffer.Mesos || second.Mesos < r.SecondOffer.Mesos {
		return domain.TradeResult{}, domain.ErrInsufficient
	}
	first.HP, first.MP = r.First.Character.HP, r.First.Character.MP
	second.HP, second.MP = r.Second.Character.HP, r.Second.Character.MP
	result := domain.TradeResult{First: domain.InventoryResult{Character: first}, Second: domain.InventoryResult{Character: second}, FirstTax: TradeTax(r.FirstOffer.Mesos), SecondTax: TradeTax(r.SecondOffer.Mesos)}
	outgoingFirst, err := takeTradeItems(ctx, tx, &result.First, r.FirstOffer.Items)
	if err != nil {
		return domain.TradeResult{}, err
	}
	outgoingSecond, err := takeTradeItems(ctx, tx, &result.Second, r.SecondOffer.Items)
	if err != nil {
		return domain.TradeResult{}, err
	}
	if err = moveTradeItems(ctx, tx, &result.First, &result.Second, outgoingFirst); err != nil {
		return domain.TradeResult{}, err
	}
	if err = moveTradeItems(ctx, tx, &result.Second, &result.First, outgoingSecond); err != nil {
		return domain.TradeResult{}, err
	}
	result.First.Character.Mesos += r.SecondOffer.Mesos - result.SecondTax - r.FirstOffer.Mesos
	result.Second.Character.Mesos += r.FirstOffer.Mesos - result.FirstTax - r.SecondOffer.Mesos
	for i, part := range []*domain.InventoryResult{&result.First, &result.Second} {
		if part.Character.Mesos < 0 || part.Character.Mesos > domain.MaxMoney() {
			return domain.TradeResult{}, domain.ErrInvalidInput
		}
		if err = commitInventoryState(ctx, tx, &part.Character); err != nil {
			return domain.TradeResult{}, err
		}
		before := first.Mesos
		if i == 1 {
			before = second.Mesos
		}
		delta := part.Character.Mesos - before
		if delta != 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'player_trade')`, "trade/"+r.ID+"/"+string(rune('0'+i)), part.Character.ID, part.Character.AccountID, part.Character.WorldID, delta, part.Character.Mesos); err != nil {
				return domain.TradeResult{}, err
			}
		}
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return domain.TradeResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_trade_settlements(trade_id,first_character_id,second_character_id,request_hash,result) VALUES($1,$2,$3,$4,$5)`, r.ID, first.ID, second.ID, tradeHash(r), raw); err != nil {
		return domain.TradeResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.TradeResult{}, err
	}
	s.roster.Invalidate(ctx, first.AccountID, first.WorldID)
	s.roster.Invalidate(ctx, second.AccountID, second.WorldID)
	return result, nil
}

func (s *Store) ResolveTradeReceipt(ctx context.Context, r domain.TradeSettlement) (domain.TradeResult, bool, error) {
	if len(r.ID) < 16 || len(r.ID) > 128 || r.First.CharacterID == r.Second.CharacterID || r.First.AccountID == r.Second.AccountID || r.First.Lease.WorldID != r.Second.Lease.WorldID {
		return domain.TradeResult{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TradeResult{}, false, err
	}
	defer tx.Rollback()
	var first, second domain.Character
	if r.First.CharacterID < r.Second.CharacterID {
		first, err = lockInventoryCharacter(ctx, tx, r.First)
		if err == nil {
			second, err = lockInventoryCharacter(ctx, tx, r.Second)
		}
	} else {
		second, err = lockInventoryCharacter(ctx, tx, r.Second)
		if err == nil {
			first, err = lockInventoryCharacter(ctx, tx, r.First)
		}
	}
	if err != nil {
		return domain.TradeResult{}, false, err
	}
	// These locks wait until the original transaction commits or rolls back.
	// The subsequent READ COMMITTED statement therefore cannot mistake an
	// in-flight original commit for an absent receipt.
	var hash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_trade_settlements WHERE trade_id=$1`, r.ID).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TradeResult{}, false, nil
	}
	if err != nil {
		return domain.TradeResult{}, false, err
	}
	if hash != tradeHash(r) {
		return domain.TradeResult{}, false, domain.ErrForbidden
	}
	var result domain.TradeResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, false, err
	}
	// A later revision needs a fresh native field/inventory snapshot, not an
	// obsolete receipt's delta. Do not publish that receipt over newer state.
	if first.Revision != result.First.Character.Revision || second.Revision != result.Second.Character.Revision {
		return domain.TradeResult{}, false, domain.ErrStaleOwner
	}
	result.First.Character = first
	result.Second.Character = second
	result.Replayed = true
	// Keep the receipt deltas: this resolves a commit response lost BEFORE
	// world publication, unlike SettleTrade's normal already-published replay.
	return result, true, nil
}

// TradeTax is the fee on the mesos one side receives. The table is the
// trade_fee_rates setting; its default is Legacy GameConstants.getTaxAmount.
func TradeTax(mesos int64) int64 { return domain.TradeFee(mesos) }

func takeTradeItems(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, items []domain.TradeItem) ([]domain.TradeItem, error) {
	return takeInventoryItems(ctx, tx, result, items, true)
}

func takeInventoryItems(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, items []domain.TradeItem, requireTradeable bool) ([]domain.TradeItem, error) {
	out := make([]domain.TradeItem, 0, len(items))
	c := &result.Character
	for _, offered := range items {
		if offered.Slot < 1 || offered.Slot > c.Capacity(offered.InvType) || offered.Quantity < 1 || offered.Quantity > 30000 || offered.TradeSlot < 1 || offered.TradeSlot > 9 {
			return nil, domain.ErrInvalidInput
		}
		if offered.InvType == 1 {
			index := -1
			for i, item := range c.Equipment {
				if item.Slot == offered.Slot && item.ID == offered.InstanceID {
					index = i
					break
				}
			}
			if index < 0 || offered.Quantity != 1 {
				return nil, domain.ErrNotFound
			}
			item := c.Equipment[index]
			definition, err := inventoryDefinition(item.ItemID)
			if err != nil || (requireTradeable && !definition.Tradeable(item.CashID)) {
				return nil, domain.ErrForbidden
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1`, item.ID); err != nil {
				return nil, err
			}
			c.Equipment = append(c.Equipment[:index], c.Equipment[index+1:]...)
			offered.Equipment = &item
			offered.Item = nil
			out = append(out, offered)
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: 1, Slot: offered.Slot})
		} else {
			index := -1
			for i, item := range c.Items {
				if item.InvType == offered.InvType && item.Slot == offered.Slot && item.ID == offered.InstanceID {
					index = i
					break
				}
			}
			if index < 0 {
				return nil, domain.ErrNotFound
			}
			item := c.Items[index]
			definition, err := inventoryDefinition(item.TemplateID)
			if err != nil || (requireTradeable && !definition.Tradeable(item.CashID)) || offered.Quantity > item.Quantity {
				return nil, domain.ErrForbidden
			}
			item.Quantity = offered.Quantity
			offered.Item = &item
			offered.Equipment = nil
			out = append(out, offered)
			left := c.Items[index].Quantity - offered.Quantity
			if left == 0 {
				if _, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
					return nil, err
				}
				c.Items = append(c.Items[:index], c.Items[index+1:]...)
				result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: offered.InvType, Slot: offered.Slot})
			} else {
				if _, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, left, item.ID); err != nil {
					return nil, err
				}
				c.Items[index].Quantity = left
				c.Items[index].Revision++
				result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: offered.InvType, Slot: offered.Slot, Quantity: left})
			}
		}
	}
	return out, nil
}

// moveTradeItems gives the items taken from one side to the other. A cash
// item's serial follows it to the receiver's account and character before
// either inventory is committed, so the giver's commit does not retire it.
func moveTradeItems(ctx context.Context, tx *sql.Tx, from, to *domain.InventoryResult, items []domain.TradeItem) error {
	moved := make(map[int64]bool)
	for _, item := range items {
		var cashID, expiry int64
		if item.Equipment != nil {
			cashID, expiry = item.Equipment.CashID, item.Equipment.ExpiresAt
		} else if item.Item != nil {
			cashID, expiry = item.Item.CashID, item.Item.ExpiresAt
		}
		if cashID > 0 {
			// One serial is one row of the offer; the trade window never
			// offers the same cash stack twice.
			if moved[cashID] {
				return domain.ErrForbidden
			}
			moved[cashID] = true
			if err := guardItemExpiry(ctx, tx, expiry); err != nil {
				return err
			}
			giver, receiver := from.Character, to.Character
			kept := false
			for _, held := range giver.Items {
				kept = kept || held.CashID == cashID
			}
			if kept {
				// Part of a stack: the part that leaves gets a serial of its
				// own and the rest keeps the original, as in storage.
				bundle := *item.Item
				if err := tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) SELECT $1,$2,$3,sn,template_id,$4,inv_type,expires_at,'inventory' FROM asset_cash_items WHERE id=$5 AND character_id=$6 AND account_id=$7 AND state='inventory' RETURNING id`, receiver.AccountID, receiver.ID, receiver.WorldID, item.Quantity, cashID, giver.ID, giver.AccountID).Scan(&bundle.CashID); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return domain.ErrStaleOwner
					}
					return err
				}
				item.Item = &bundle
			} else {
				updated, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET character_id=$1,account_id=$2,world_id=$3,slot=0 WHERE id=$4 AND character_id=$5 AND account_id=$6 AND state='inventory'`, receiver.ID, receiver.AccountID, receiver.WorldID, cashID, giver.ID, giver.AccountID)
				if err != nil {
					return err
				}
				if n, _ := updated.RowsAffected(); n != 1 {
					return domain.ErrStaleOwner
				}
			}
		}
		if err := receiveTradeItem(ctx, tx, to, item); err != nil {
			return err
		}
	}
	return nil
}

func receiveTradeItem(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, item domain.TradeItem) error {
	if item.Equipment != nil {
		e := *item.Equipment
		e.Slot = FreeEquipmentSlot(&result.Character)
		if e.Slot == 0 {
			return domain.ErrInsufficient
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_equipment(id,character_id,item_id,slot,str,dex,int_stat,luk,hp,mp,watk,matk,wdef,mdef,acc,avoid,speed,jump,upgrade_slots) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, e.ID, result.Character.ID, e.ItemID, e.Slot, e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots); err != nil {
			return err
		}
		result.Character.Equipment = append(result.Character.Equipment, e)
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: 1, Slot: e.Slot, Equipment: &e})
		return nil
	}
	if item.Item == nil {
		return domain.ErrInvalidInput
	}
	if item.Item.CashID > 0 {
		return receiveCashBundle(ctx, tx, result, *item.Item)
	}
	return pickupBundle(ctx, tx, domain.ItemDrop{InvType: item.InvType, TemplateID: item.Item.TemplateID, Quantity: item.Quantity}, result)
}
