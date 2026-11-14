package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"maple-go-server/internal/domain"
)

var _ domain.AdminInventoryRepository = (*Store)(nil)

// lockOfflineCharacter locks a character nobody is playing. Account before
// character, the order every world authority uses; the session row lock keeps
// a login from claiming the character until this transaction ends.
func lockOfflineCharacter(ctx context.Context, tx *sql.Tx, characterID int64) (domain.Character, error) {
	var accountID int64
	err := tx.QueryRowContext(ctx, `SELECT account_id FROM game_characters WHERE id=$1`, characterID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Character{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM auth_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&accountID); err != nil {
		return domain.Character{}, err
	}
	var state, instance string
	var playing, epoch int64
	var world int
	err = tx.QueryRowContext(ctx, `SELECT state,character_id,world_id,owner_instance,world_epoch FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&state, &playing, &world, &instance, &epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, err
	}
	// The session row is locked from here to the commit, so the account can
	// neither enter a world nor leave one while this edit is being made.
	if state == "world" && playing == characterID {
		var alive bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ops_runtime_leases WHERE world_id=$1 AND instance_id=$2 AND epoch=$3 AND expires_at>clock_timestamp())`, world, instance, epoch).Scan(&alive); err != nil {
			return domain.Character{}, err
		}
		if alive {
			return domain.Character{}, domain.ErrAlreadyOnline
		}
	}
	// A character on its way into a world or inside the cash shop is loaded
	// somewhere this transaction cannot see.
	if state == "transfer" || state == "cashshop" {
		return domain.Character{}, domain.ErrAlreadyOnline
	}
	// A world whose lease is alive still holds the character in memory. One
	// whose lease has lapsed can no longer write: its saves are fenced.
	var held bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ops_character_ownership o JOIN ops_runtime_leases l ON l.world_id=o.world_id AND l.instance_id=o.instance_id AND l.epoch=o.epoch AND l.expires_at>clock_timestamp() WHERE o.character_id=$1)`, characterID).Scan(&held); err != nil {
		return domain.Character{}, err
	}
	if held {
		return domain.Character{}, domain.ErrAlreadyOnline
	}
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT account_id FROM game_characters WHERE id=$1 FOR UPDATE`, characterID).Scan(&owner); err != nil {
		return domain.Character{}, err
	}
	if owner != accountID {
		return domain.Character{}, domain.ErrStaleOwner
	}
	return readCharacterSQL(ctx, tx, characterID)
}

func validAdminActor(actor string) bool {
	return strings.TrimSpace(actor) == actor && len(actor) >= 1 && len(actor) <= 128
}

type adminInventoryReceipt struct {
	Changes  []domain.InventoryChange `json:"changes"`
	Worn     bool                     `json:"worn,omitempty"`
	Revision int64                    `json:"revision"`
}

func worn(slot int) bool { return slot < 0 }

// ApplyAdminInventory changes one thing in a character's bags. With a session
// it runs under that world's authority, like any command of the player; with
// none it requires the character to be offline. A repeated operation ID
// returns the first outcome and changes nothing.
func (s *Store) ApplyAdminInventory(ctx context.Context, r domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
	if !validGMOperation(r.OperationID) || !validAdminActor(r.Actor) || r.CharacterID < 1 {
		return domain.AdminInventoryResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AdminInventoryResult{}, err
	}
	defer tx.Rollback()
	var c domain.Character
	if r.Session != nil {
		if r.Session.CharacterID != r.CharacterID {
			return domain.AdminInventoryResult{}, domain.ErrInvalidInput
		}
		c, err = lockInventoryCharacter(ctx, tx, *r.Session)
	} else {
		c, err = lockOfflineCharacter(ctx, tx, r.CharacterID)
	}
	if err != nil {
		return domain.AdminInventoryResult{}, err
	}
	var priorCharacter int64
	var priorKind string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT character_id,kind,result FROM ops_admin_inventory_ops WHERE operation_id=$1`, r.OperationID).Scan(&priorCharacter, &priorKind, &raw)
	if err == nil {
		var receipt adminInventoryReceipt
		if priorCharacter != c.ID || priorKind != r.Kind {
			return domain.AdminInventoryResult{}, domain.ErrForbidden
		}
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return domain.AdminInventoryResult{}, err
		}
		return domain.AdminInventoryResult{Character: c, Changes: receipt.Changes, Worn: receipt.Worn, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.AdminInventoryResult{}, err
	}
	if r.Session != nil {
		// The world's copy must be the one this change is made to.
		if c.Revision != r.Session.Character.Revision || c.MapID != r.Session.Character.MapID {
			return domain.AdminInventoryResult{}, domain.ErrStaleOwner
		}
		c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
		if c.HP < 0 || c.MP < 0 {
			return domain.AdminInventoryResult{}, domain.ErrInvalidInput
		}
	}
	result := domain.AdminInventoryResult{Character: c}
	var before any
	oldHP, oldMP := equipmentVitals(c.Equipment)
	switch r.Kind {
	case domain.AdminSetEquipment:
		before, err = adminSetEquipment(ctx, tx, r, &result)
	case domain.AdminSetQuantity:
		before, err = adminSetQuantity(ctx, tx, r, &result)
	case domain.AdminRemove:
		before, err = adminRemove(ctx, tx, r, &result)
	case domain.AdminGiveItem:
		err = adminGiveItem(ctx, tx, r, &result)
	case domain.AdminGiveMesos:
		before = map[string]int64{"mesos": c.Mesos}
		err = adminGiveMesos(ctx, tx, r, &result)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return domain.AdminInventoryResult{}, err
	}
	next := &result.Character
	if result.Worn {
		if err = recomputeEquipmentVitals(next, oldHP, oldMP); err != nil {
			return domain.AdminInventoryResult{}, err
		}
	}
	// The world carried vitals that may now exceed a lowered maximum.
	next.HP, next.MP = min(next.HP, next.MaxHP), min(next.MP, next.MaxMP)
	if err = commitInventoryState(ctx, tx, next); err != nil {
		return domain.AdminInventoryResult{}, err
	}
	if before == nil {
		before = struct{}{}
	}
	request, beforeRaw, resultRaw, err := marshalThree(r, before, adminInventoryReceipt{Changes: result.Changes, Worn: result.Worn, Revision: next.Revision})
	if err != nil {
		return domain.AdminInventoryResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_admin_inventory_ops(operation_id,actor,account_id,character_id,online,kind,request,before_state,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.OperationID, r.Actor, c.AccountID, c.ID, r.Session != nil, r.Kind, request, beforeRaw, resultRaw); err != nil {
		return domain.AdminInventoryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.AdminInventoryResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

func marshalThree(a, b, c any) (x, y, z []byte, err error) {
	if x, err = json.Marshal(a); err != nil {
		return
	}
	if y, err = json.Marshal(b); err != nil {
		return
	}
	z, err = json.Marshal(c)
	return
}

func adminSetEquipment(ctx context.Context, tx *sql.Tx, r domain.AdminInventoryRequest, result *domain.AdminInventoryResult) (any, error) {
	c := &result.Character
	if r.Equipment == nil || r.InvType != 1 {
		return nil, domain.ErrInvalidInput
	}
	n := *r.Equipment
	for _, v := range []int{n.STR, n.DEX, n.INT, n.LUK, n.HP, n.MP, n.WATK, n.MATK, n.WDEF, n.MDEF, n.ACC, n.AVOID, n.Speed, n.Jump} {
		if v < 0 || v > domain.AdminEquipmentStatMax {
			return nil, domain.ErrInvalidInput
		}
	}
	if n.UpgradeSlots < 0 || n.UpgradeSlots > domain.AdminEquipmentByteMax || n.ScrollLevel < 0 || n.ScrollLevel > domain.AdminEquipmentByteMax {
		return nil, domain.ErrInvalidInput
	}
	for i := range c.Equipment {
		e := c.Equipment[i]
		if e.ID != r.AssetID {
			continue
		}
		if !r.Expect.MatchesEquipment(e) {
			return nil, domain.ErrAdminChanged
		}
		before := e
		e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP = n.STR, n.DEX, n.INT, n.LUK, n.HP, n.MP
		e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump = n.WATK, n.MATK, n.WDEF, n.MDEF, n.ACC, n.AVOID, n.Speed, n.Jump
		e.UpgradeSlots, e.ScrollLevel = n.UpgradeSlots, n.ScrollLevel
		if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment SET str=$1,dex=$2,int_stat=$3,luk=$4,hp=$5,mp=$6,watk=$7,matk=$8,wdef=$9,mdef=$10,acc=$11,avoid=$12,speed=$13,jump=$14,upgrade_slots=$15 WHERE id=$16 AND character_id=$17`,
			e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots, e.ID, c.ID); err != nil {
			return nil, err
		}
		// The scroll count is equipment metadata; commitInventoryState stores it.
		c.Equipment[i] = e
		result.Worn = worn(e.Slot)
		// The client has no "stats changed" update: take the item out and put
		// the new one in the same slot, as a scroll does.
		result.Changes = []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: e.Slot}, {Mode: 0, InvType: 1, Slot: e.Slot, Equipment: &e}}
		return before, nil
	}
	return nil, domain.ErrNotFound
}

func adminSetQuantity(ctx context.Context, tx *sql.Tx, r domain.AdminInventoryRequest, result *domain.AdminInventoryResult) (any, error) {
	c := &result.Character
	if r.InvType < 2 || r.InvType > 5 {
		return nil, domain.ErrInvalidInput
	}
	for i := range c.Items {
		item := c.Items[i]
		if item.ID != r.AssetID || item.InvType != r.InvType {
			continue
		}
		definition, err := inventoryDefinition(item.TemplateID)
		if err != nil || domain.IsPetTemplate(item.TemplateID) {
			return nil, domain.ErrInvalidInput
		}
		if !r.Expect.MatchesStack(item) {
			return nil, domain.ErrAdminChanged
		}
		// A stack already above the template's limit may keep its size.
		if r.Quantity < 1 || r.Quantity > max(definition.SlotMax, item.Quantity) {
			return nil, domain.ErrInvalidInput
		}
		before := item
		item.Quantity = r.Quantity
		item.Revision++
		if _, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2 AND character_id=$3`, item.Quantity, item.ID, c.ID); err != nil {
			return nil, err
		}
		c.Items[i] = item
		result.Changes = []domain.InventoryChange{{Mode: 1, InvType: item.InvType, Slot: item.Slot, Quantity: item.Quantity}}
		return before, nil
	}
	return nil, domain.ErrNotFound
}

func adminRemove(ctx context.Context, tx *sql.Tx, r domain.AdminInventoryRequest, result *domain.AdminInventoryResult) (any, error) {
	c := &result.Character
	if r.InvType == 1 {
		for i, e := range c.Equipment {
			if e.ID != r.AssetID {
				continue
			}
			if !r.Expect.MatchesEquipment(e) {
				return nil, domain.ErrAdminChanged
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_equipment_metadata WHERE equipment_id=$1`, e.ID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1 AND character_id=$2`, e.ID, c.ID); err != nil {
				return nil, err
			}
			c.Equipment = append(append([]domain.Equipment(nil), c.Equipment[:i]...), c.Equipment[i+1:]...)
			result.Worn = worn(e.Slot)
			result.Changes = []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: e.Slot}}
			return e, nil
		}
		return nil, domain.ErrNotFound
	}
	if r.InvType < 2 || r.InvType > 5 {
		return nil, domain.ErrInvalidInput
	}
	for i, item := range c.Items {
		if item.ID != r.AssetID || item.InvType != r.InvType {
			continue
		}
		// A pet has its own records and may be out on the map.
		if domain.IsPetTemplate(item.TemplateID) {
			return nil, domain.ErrInvalidInput
		}
		if !r.Expect.MatchesStack(item) {
			return nil, domain.ErrAdminChanged
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1 AND character_id=$2`, item.ID, c.ID); err != nil {
			return nil, err
		}
		c.Items = append(append([]domain.InventoryItem(nil), c.Items[:i]...), c.Items[i+1:]...)
		result.Changes = []domain.InventoryChange{{Mode: 3, InvType: item.InvType, Slot: item.Slot}}
		return item, nil
	}
	return nil, domain.ErrNotFound
}

func freeBundleSlot(c *domain.Character, invType int) int {
	for slot := 1; slot <= c.Capacity(invType); slot++ {
		free := true
		for _, item := range c.Items {
			if item.InvType == invType && item.Slot == slot {
				free = false
				break
			}
		}
		if free {
			return slot
		}
	}
	return 0
}

// mintCashItem gives an item in a bag the cash serial every cash item must
// carry. Like a granted pet it has no shop product behind it.
func mintCashItem(ctx context.Context, tx *sql.Tx, c *domain.Character, template, invType, slot, quantity int) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,0,$4,$5,$6,$7,0,'inventory') RETURNING id`,
		c.AccountID, c.ID, c.WorldID, template, quantity, invType, slot).Scan(&id)
	return id, err
}

func adminGiveItem(ctx context.Context, tx *sql.Tx, r domain.AdminInventoryRequest, result *domain.AdminInventoryResult) error {
	c := &result.Character
	if r.ItemID < 1000000 || r.ItemID > 5999999 || r.Quantity < 1 || r.Quantity > domain.AdminGiveMax {
		return domain.ErrInvalidInput
	}
	definition, err := inventoryDefinition(r.ItemID)
	if err != nil {
		return domain.ErrNotFound
	}
	// A pet needs a name and its own state; the pet grant handles those.
	if domain.IsPetTemplate(r.ItemID) {
		return domain.ErrInvalidInput
	}
	if definition.InvType == 1 {
		for n := 0; n < r.Quantity; n++ {
			e := definition.Equipment
			if e.Slot = FreeEquipmentSlot(c); e.Slot == 0 {
				return domain.ErrInsufficient
			}
			if err = insertEquipment(ctx, tx, c.ID, &e); err != nil {
				return err
			}
			if definition.Cash {
				if e.CashID, err = mintCashItem(ctx, tx, c, e.ItemID, 1, e.Slot, 1); err != nil {
					return err
				}
			}
			c.Equipment = append(c.Equipment, e)
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: 1, Slot: e.Slot, Equipment: &e})
		}
		return nil
	}
	if !definition.Cash {
		inv := domain.InventoryResult{Character: *c}
		if err = pickupBundle(ctx, tx, domain.ItemDrop{InvType: definition.InvType, TemplateID: definition.ID, Quantity: r.Quantity}, &inv); err != nil {
			return err
		}
		*c = inv.Character
		result.Changes = inv.Changes
		return nil
	}
	// Cash stacks never merge: each has its own serial.
	for remaining := r.Quantity; remaining > 0; {
		item := domain.InventoryItem{TemplateID: definition.ID, InvType: definition.InvType, Quantity: min(remaining, definition.SlotMax), Revision: 1}
		if item.Slot = freeBundleSlot(c, item.InvType); item.Slot == 0 {
			return domain.ErrInsufficient
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id`, c.ID, item.InvType, item.Slot, item.TemplateID, item.Quantity).Scan(&item.ID); err != nil {
			return err
		}
		if item.CashID, err = mintCashItem(ctx, tx, c, item.TemplateID, item.InvType, item.Slot, item.Quantity); err != nil {
			return err
		}
		remaining -= item.Quantity
		c.Items = append(c.Items, item)
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: item.InvType, Slot: item.Slot, Item: &item})
	}
	return nil
}

func adminGiveMesos(ctx context.Context, tx *sql.Tx, r domain.AdminInventoryRequest, result *domain.AdminInventoryResult) error {
	c := &result.Character
	if r.Mesos == 0 {
		return domain.ErrInvalidInput
	}
	if r.Mesos > 0 && r.Mesos > domain.MaxMoney()-c.Mesos {
		return domain.ErrInvalidInput
	}
	if r.Mesos < 0 && -r.Mesos > c.Mesos {
		return domain.ErrInsufficient
	}
	c.Mesos += r.Mesos
	// commitInventoryState writes the balance with the new revision.
	_, err := tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'web_admin')`, r.OperationID, c.ID, c.AccountID, c.WorldID, r.Mesos, c.Mesos)
	return err
}

// ReadAdminWallet reads an account's cash balances without creating a wallet.
func (s *Store) ReadAdminWallet(ctx context.Context, accountID int64) (domain.CashWallet, error) {
	var w domain.CashWallet
	err := s.db.QueryRowContext(ctx, `SELECT credit,points,prepaid FROM asset_cash_wallets WHERE account_id=$1`, accountID).Scan(&w.Credit, &w.Points, &w.Prepaid)
	if errors.Is(err, sql.ErrNoRows) {
		return w, nil
	}
	return w, err
}

// AdjustAdminWallet adds to (or with a negative delta takes from) an account's
// Gash or maple points. The cash shop reads the wallet under the same row
// lock for every purchase, so no session has to be told.
func (s *Store) AdjustAdminWallet(ctx context.Context, r domain.AdminWalletRequest) (domain.CashWallet, error) {
	if !validGMOperation(r.OperationID) || !validAdminActor(r.Actor) || r.AccountID < 1 || r.Delta == 0 || r.Currency != domain.CashCurrencyCredit && r.Currency != domain.CashCurrencyPoints {
		return domain.CashWallet{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashWallet{}, err
	}
	defer tx.Rollback()
	var account int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM auth_accounts WHERE id=$1 FOR KEY SHARE`, r.AccountID).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CashWallet{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CashWallet{}, err
	}
	wallet, err := readCashWalletSQL(ctx, tx, r.AccountID)
	if err != nil {
		return domain.CashWallet{}, err
	}
	var prior int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM ops_admin_inventory_ops WHERE operation_id=$1`, r.OperationID).Scan(&prior)
	if err == nil {
		if prior != r.AccountID {
			return domain.CashWallet{}, domain.ErrForbidden
		}
		return wallet, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CashWallet{}, err
	}
	before := wallet
	balance := &wallet.Credit
	if r.Currency == domain.CashCurrencyPoints {
		balance = &wallet.Points
	}
	if r.Delta > 0 && r.Delta > domain.MaxMoney()-*balance {
		return domain.CashWallet{}, domain.ErrInvalidInput
	}
	if r.Delta < 0 && -r.Delta > *balance {
		return domain.CashWallet{}, domain.ErrInsufficient
	}
	*balance += r.Delta
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=$1,points=$2 WHERE account_id=$3`, wallet.Credit, wallet.Points, r.AccountID); err != nil {
		return domain.CashWallet{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_ledger(operation_id,account_id,currency,delta,balance_after) VALUES($1,$2,$3,$4,$5)`, r.OperationID, r.AccountID, r.Currency, r.Delta, *balance); err != nil {
		return domain.CashWallet{}, err
	}
	request, beforeRaw, resultRaw, err := marshalThree(r, before, wallet)
	if err != nil {
		return domain.CashWallet{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_admin_inventory_ops(operation_id,actor,account_id,character_id,online,kind,request,before_state,result) VALUES($1,$2,$3,$4,false,'wallet',$5,$6,$7)`,
		r.OperationID, r.Actor, r.AccountID, r.CharacterID, request, beforeRaw, resultRaw); err != nil {
		return domain.CashWallet{}, err
	}
	return wallet, tx.Commit()
}
