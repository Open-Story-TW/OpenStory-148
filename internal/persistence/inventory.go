package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.InventoryRepository = (*Store)(nil)

func validInventoryMove(r domain.InventoryMoveRequest) bool {
	if r.InvType < 1 || r.InvType > 5 || r.Source == 0 || r.Source == r.Destination || r.Source > domain.MaxInventorySlots || r.Destination > domain.MaxInventorySlots || r.Source < -199 || r.Destination < -199 {
		return false
	}
	if r.InvType != 1 && (r.Source < 0 || r.Destination < 0) {
		return false
	}
	return r.Destination != 0 || (r.Source > 0 && r.Quantity > 0 && r.Quantity <= 30000)
}

func inventoryDefinition(itemID int) (content.Item, error) {
	return content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), itemID)
}

func canEquip(c domain.Character, itemID, slot int) bool {
	item, err := inventoryDefinition(itemID)
	layer := 0
	if slot < -100 {
		layer = 100
	}
	if err != nil || item.InvType != 1 || item.Cash != (layer == 100) || !item.AllowsSlot(slot+layer) || c.Level < item.ReqLevel || (item.Gender >= 0 && item.Gender != c.Gender) {
		return false
	}
	str, dex, intelligence, luk := c.STR, c.DEX, c.INT, c.LUK
	for _, e := range c.Equipment {
		if e.Slot < 0 && e.Slot != slot {
			str += e.STR
			dex += e.DEX
			intelligence += e.INT
			luk += e.LUK
		}
	}
	if str < item.ReqSTR || dex < item.ReqDEX || intelligence < item.ReqINT || luk < item.ReqLUK {
		return false
	}
	if itemID/10000 == 134 && (c.Job < 430 || c.Job > 434) {
		return false // A katara is the Dual Blade's off-hand only.
	}
	if item.ReqJob == -1 && !domain.IsBeginnerJob(c.Job) {
		return false
	}
	if item.ReqJob > 0 {
		// reqJob bits are job categories (1 warrior .. 16 pirate), shared by
		// every family: 1100 Dawn Warrior, 2100 Aran, 3300 Wild Hunter, ...
		job := c.Job % 1000 / 100
		if job < 1 || job > 5 || item.ReqJob&(1<<(job-1)) == 0 {
			return false
		}
	}
	// Overalls require both clothing slots. Unsupported automatic relocations
	// are rejected rather than silently destroying the conflicting garment.
	for _, e := range c.Equipment {
		if (item.Overall && e.Slot == -6-layer) || (slot == -6-layer && e.Slot == -5-layer && e.ItemID/10000 == 105) {
			return false
		}
	}
	return true
}

// lockInventoryCharacter shares the established fencing/lock order, so a
// concurrent disconnect or transfer cannot race an asset transaction.
func lockInventoryCharacter(ctx context.Context, tx *sql.Tx, session domain.CharacterSession) (domain.Character, error) {
	if session.CharacterID <= 0 || session.AccountID <= 0 || session.Character.ID != session.CharacterID {
		return domain.Character{}, domain.ErrForbidden
	}
	if err := guardLease(ctx, tx, session.Lease); err != nil {
		return domain.Character{}, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR SHARE`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
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
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, session.CharacterID).Scan(&id); err != nil {
		return domain.Character{}, err
	}
	value, err := readCharacterSQL(ctx, tx, session.CharacterID)
	if err == nil && (value.AccountID != session.AccountID || value.WorldID != session.Lease.WorldID) {
		err = domain.ErrForbidden
	}
	return value, err
}

func (s *Store) MoveInventory(ctx context.Context, r domain.InventoryMoveRequest) (domain.InventoryResult, error) {
	if !validInventoryMove(r) {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	var invType, source, destination, quantity int
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT inv_type,source_slot,destination_slot,quantity,result FROM asset_inventory_commands WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4`, value.ID, r.Session.SessionID, r.Session.Generation, int64(r.ClientTick)).Scan(&invType, &source, &destination, &quantity, &prior)
	if err == nil {
		if invType != r.InvType || source != r.Source || destination != r.Destination || quantity != r.Quantity {
			return domain.InventoryResult{}, domain.ErrForbidden
		}
		var result domain.InventoryResult
		if err = json.Unmarshal(prior, &result); err != nil {
			return domain.InventoryResult{}, err
		}
		result.Character = value
		result.Replayed = true
		result.Changes = nil
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.InventoryResult{}, err
	}
	if value.Revision != r.Session.Character.Revision {
		return domain.InventoryResult{}, domain.ErrStaleOwner
	}
	if r.Source > value.Capacity(r.InvType) || r.Destination > value.Capacity(r.InvType) {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	for _, item := range value.Equipment {
		if r.InvType == 1 && (item.Slot == r.Source || item.Slot == r.Destination) {
			if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
				return domain.InventoryResult{}, err
			}
		}
	}
	for _, item := range value.Items {
		if item.InvType == r.InvType && (item.Slot == r.Source || item.Slot == r.Destination) {
			if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
				return domain.InventoryResult{}, err
			}
		}
	}
	value.HP, value.MP = r.Session.Character.HP, r.Session.Character.MP
	if value.HP < 0 || value.HP > value.MaxHP || value.MP < 0 || value.MP > value.MaxMP {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	result := domain.InventoryResult{Character: value}
	if r.Destination == 0 {
		err = s.dropInventory(ctx, tx, r, &result)
	} else if r.InvType == 1 {
		err = moveEquipment(ctx, tx, r, &result)
	} else {
		err = moveBundle(ctx, tx, r, &result)
	}
	if err != nil {
		return domain.InventoryResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &result.Character); err != nil {
		return domain.InventoryResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_inventory_commands(character_id,session_id,generation,client_tick,inv_type,source_slot,destination_slot,quantity,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.ID, r.Session.SessionID, r.Session.Generation, int64(r.ClientTick), r.InvType, r.Source, r.Destination, r.Quantity, raw)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.InventoryResult{}, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return result, nil
}

func moveEquipment(ctx context.Context, tx *sql.Tx, r domain.InventoryMoveRequest, result *domain.InventoryResult) error {
	c := &result.Character
	src, dst := -1, -1
	for i, e := range c.Equipment {
		if e.Slot == r.Source {
			src = i
		}
		if e.Slot == r.Destination {
			dst = i
		}
	}
	if src < 0 {
		return domain.ErrNotFound
	}
	if r.Destination < 0 && !canEquip(*c, c.Equipment[src].ItemID, r.Destination) {
		return domain.ErrInvalidInput
	}
	if r.Source < 0 && dst >= 0 && !canEquip(*c, c.Equipment[dst].ItemID, r.Source) {
		return domain.ErrInvalidInput
	}
	oldHP, oldMP := equipmentVitals(c.Equipment)
	if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment SET slot=$1 WHERE id=$2`, r.Destination, c.Equipment[src].ID); err != nil {
		return err
	}
	c.Equipment[src].Slot = r.Destination
	if dst >= 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment SET slot=$1 WHERE id=$2`, r.Source, c.Equipment[dst].ID); err != nil {
			return err
		}
		c.Equipment[dst].Slot = r.Source
	}
	if err := recomputeEquipmentVitals(c, oldHP, oldMP); err != nil {
		return err
	}
	c.HP = min(c.HP, c.MaxHP)
	c.MP = min(c.MP, c.MaxMP)
	result.Changes = []domain.InventoryChange{{Mode: 2, InvType: 1, Slot: r.Source, Destination: r.Destination}}
	return nil
}

func equipmentVitals(equipment []domain.Equipment) (hp, mp int) {
	for _, e := range equipment {
		if e.Slot < 0 && e.Slot >= -199 {
			hp += e.HP
			mp += e.MP
		}
	}
	return
}

func moveBundle(ctx context.Context, tx *sql.Tx, r domain.InventoryMoveRequest, result *domain.InventoryResult) error {
	c := &result.Character
	src, dst := -1, -1
	for i, item := range c.Items {
		if item.InvType != r.InvType {
			continue
		}
		if item.Slot == r.Source {
			src = i
		}
		if item.Slot == r.Destination {
			dst = i
		}
	}
	if src < 0 {
		return domain.ErrNotFound
	}
	a := c.Items[src]
	if dst >= 0 && c.Items[dst].TemplateID == a.TemplateID && a.CashID == 0 && c.Items[dst].CashID == 0 && a.ExpiresAt == c.Items[dst].ExpiresAt {
		definition, err := inventoryDefinition(a.TemplateID)
		if err != nil || definition.InvType != r.InvType {
			return domain.ErrInvalidInput
		}
		amount := min(a.Quantity, definition.SlotMax-c.Items[dst].Quantity)
		if amount <= 0 {
			return domain.ErrInvalidInput
		}
		c.Items[src].Quantity -= amount
		c.Items[dst].Quantity += amount
		c.Items[src].Revision++
		c.Items[dst].Revision++
		if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, c.Items[dst].Quantity, c.Items[dst].ID); err != nil {
			return err
		}
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: r.InvType, Slot: r.Destination, Quantity: c.Items[dst].Quantity})
		if c.Items[src].Quantity == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, a.ID); err != nil {
				return err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: r.InvType, Slot: r.Source})
			c.Items = append(c.Items[:src], c.Items[src+1:]...)
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, c.Items[src].Quantity, a.ID); err != nil {
				return err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: r.InvType, Slot: r.Source, Quantity: c.Items[src].Quantity})
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET slot=$1,revision=revision+1 WHERE id=$2`, r.Destination, a.ID); err != nil {
			return err
		}
		c.Items[src].Slot = r.Destination
		c.Items[src].Revision++
		if dst >= 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET slot=$1,revision=revision+1 WHERE id=$2`, r.Source, c.Items[dst].ID); err != nil {
				return err
			}
			c.Items[dst].Slot = r.Source
			c.Items[dst].Revision++
		}
		result.Changes = []domain.InventoryChange{{Mode: 2, InvType: r.InvType, Slot: r.Source, Destination: r.Destination}}
	}
	return nil
}

func commitInventoryState(ctx context.Context, tx *sql.Tx, c *domain.Character) error {
	if c.VitalsModel == 1 {
		if err := recomputeVitals(c); err != nil {
			return err
		}
		// A timed maximum buff can expire during WZ/RNG/SQL work. Clamp
		// against this final authoritative derivation before persisting the
		// character and roster; never restore HP/MP or infer a legacy base.
		c.HP = min(c.HP, c.MaxHP)
		c.MP = min(c.MP, c.MaxMP)
	}
	if err := syncCashLocations(ctx, tx, *c); err != nil {
		return err
	}
	if err := persistEquipmentMetadata(ctx, tx, *c); err != nil {
		return err
	}
	c.Revision++
	if err := persistBaseVitals(ctx, tx, *c); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_characters SET hp=$1,mp=$2,max_hp=$3,max_mp=$4,revision=$5,mesos=$6 WHERE id=$7`, c.HP, c.MP, c.MaxHP, c.MaxMP, c.Revision, c.Mesos, c.ID); err != nil {
		return err
	}
	raw, err := marshalRoster(*c)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1,revision=$2 WHERE character_id=$3`, raw, c.Revision, c.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type,account_id,world_id) VALUES('roster.invalidate',$1,$2)`, c.AccountID, c.WorldID)
	return err
}
