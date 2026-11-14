package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.EnhancementRepository = (*Store)(nil)
var _ domain.ScrollOutcomeRepository = (*Store)(nil)

func scrollRequestHash(r domain.ScrollRequest) string {
	v := struct {
		Source, Target, White int
		Legendary             bool
	}{r.SourceSlot, r.TargetSlot, r.WhiteFlags, r.LegendarySpirit}
	raw, _ := json.Marshal(v)
	return tokenHash(string(raw))
}

func rollScroll() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

func consumeScrollMaterial(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, index int) error {
	item := result.Character.Items[index]
	if err := guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
		return err
	}
	left := item.Quantity - 1
	if left < 0 {
		return domain.ErrInsufficient
	}
	if left == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
			return err
		}
		result.Character.Items = append(result.Character.Items[:index], result.Character.Items[index+1:]...)
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: item.InvType, Slot: item.Slot})
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, left, item.ID); err != nil {
			return err
		}
		result.Character.Items[index].Quantity = left
		result.Character.Items[index].Revision++
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: item.InvType, Slot: item.Slot, Quantity: left})
	}
	return nil
}

func addScrollStats(e *domain.Equipment, d domain.Equipment) error {
	pairs := []struct {
		p     *int
		delta int
	}{{&e.STR, d.STR}, {&e.DEX, d.DEX}, {&e.INT, d.INT}, {&e.LUK, d.LUK}, {&e.HP, d.HP}, {&e.MP, d.MP}, {&e.WATK, d.WATK}, {&e.MATK, d.MATK}, {&e.WDEF, d.WDEF}, {&e.MDEF, d.MDEF}, {&e.ACC, d.ACC}, {&e.AVOID, d.AVOID}, {&e.Speed, d.Speed}, {&e.Jump, d.Jump}}
	for _, p := range pairs {
		if *p.p < 0 || p.delta < 0 || *p.p > 32767-p.delta {
			return domain.ErrInvalidInput
		}
	}
	for _, p := range pairs {
		*p.p += p.delta
	}
	return nil
}

func (s *Store) ApplyScroll(ctx context.Context, r domain.ScrollRequest) (domain.ScrollResult, error) {
	return s.applyScroll(ctx, r, rollScroll, (*sql.Tx).Commit)
}

// Per-call seams permit real commit-with-lost-reply tests without a mutable
// process-wide RNG/commit override. Resolution never calls either function.
func (s *Store) applyScroll(ctx context.Context, r domain.ScrollRequest, randN func() (int, error), commit func(*sql.Tx) error) (domain.ScrollResult, error) {
	if r.SourceSlot < 1 || r.SourceSlot > domain.MaxInventorySlots || r.TargetSlot == 0 || r.TargetSlot < -199 || r.TargetSlot > domain.MaxInventorySlots || (r.WhiteFlags != 0 && r.WhiteFlags != 2) {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	if randN == nil || commit == nil || r.Session.CharacterID <= 0 || r.Session.AccountID <= 0 || r.Session.Character.ID != r.Session.CharacterID {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ScrollResult{}, err
	}
	defer tx.Rollback()
	_, err = lockPotentialIdentity(ctx, tx, domain.PotentialCommandRequest{Session: r.Session})
	if err != nil {
		return domain.ScrollResult{}, err
	}
	c, err := readCharacterSQL(ctx, tx, r.Session.CharacterID)
	if err != nil {
		return domain.ScrollResult{}, err
	}
	prior, found, err := readScrollReceipt(ctx, tx, r, c, false)
	if found || err != nil {
		return prior, err
	}
	if c.Revision != r.Session.Character.Revision || c.MapID != r.Session.Character.MapID {
		return domain.ScrollResult{}, domain.ErrStaleOwner
	}
	if r.SourceSlot > c.Capacity(2) || r.TargetSlot > c.Capacity(1) {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	if r.Session.Character.HP <= 0 || r.Session.Character.HP > c.MaxHP || r.Session.Character.MP < 0 || r.Session.Character.MP > c.MaxMP {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	if r.TargetSlot > 0 {
		spirit := false
		for _, skill := range c.Skills {
			if skill.ID == 1003 && skill.Level > 0 {
				spirit = true
			}
		}
		if !r.LegendarySpirit || !spirit {
			return domain.ScrollResult{}, domain.ErrForbidden
		}
	} else if r.LegendarySpirit {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	materialIndex, equipmentIndex := -1, -1
	for i, item := range c.Items {
		if item.InvType == 2 && item.Slot == r.SourceSlot {
			materialIndex = i
			break
		}
	}
	for i, item := range c.Equipment {
		if item.Slot == r.TargetSlot {
			equipmentIndex = i
			break
		}
	}
	if materialIndex < 0 || equipmentIndex < 0 {
		return domain.ScrollResult{}, domain.ErrNotFound
	}
	material, eq := c.Items[materialIndex], c.Equipment[equipmentIndex]
	plan := domain.ScrollOutcomePlan{Version: 1, SessionID: r.Session.SessionID, Generation: r.Session.Generation, CharacterID: c.ID, AccountID: c.AccountID, WorldID: c.WorldID, ChannelID: r.Session.ChannelID, MapID: c.MapID, ClientTick: r.ClientTick, SourceSlot: r.SourceSlot, TargetSlot: r.TargetSlot, WhiteFlags: r.WhiteFlags, LegendarySpirit: r.LegendarySpirit, BeforeRevision: c.Revision, MaterialBefore: material, EquipmentBefore: eq}
	if material.Quantity < 1 || eq.UpgradeSlots < 1 || eq.ScrollLevel >= 255 {
		return domain.ScrollResult{}, domain.ErrInsufficient
	}
	if err = guardItemExpiry(ctx, tx, eq.ExpiresAt); err != nil {
		return domain.ScrollResult{}, err
	}
	// Protection flags and their consumption rules are a separate increment.
	if eq.Flags&(0x100|0x200|0x2000|0x4000) != 0 {
		return domain.ScrollResult{}, domain.ErrForbidden
	}
	definition, err := inventoryDefinition(eq.ItemID)
	if err != nil || definition.Cash {
		return domain.ScrollResult{}, domain.ErrForbidden
	}
	scroll, err := content.ScrollDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), material.TemplateID)
	if err != nil || scroll.TargetCategory != eq.ItemID/10000%100 {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	if len(scroll.RequiredItems) > 0 {
		allowed := false
		for _, id := range scroll.RequiredItems {
			if id == eq.ItemID {
				allowed = true
			}
		}
		if !allowed {
			return domain.ScrollResult{}, domain.ErrForbidden
		}
	}
	if r.WhiteFlags == 2 {
		found := false
		for _, item := range c.Items {
			if item.InvType == 2 && item.TemplateID == 2340000 && item.Quantity > 0 {
				found = true
				if plan.WhiteBefore == nil {
					copy := item
					plan.WhiteBefore = &copy
				}
			}
		}
		if !found {
			return domain.ScrollResult{}, domain.ErrInsufficient
		}
	}
	result := domain.ScrollResult{Inventory: domain.InventoryResult{Character: c}, ScrollID: material.TemplateID, EquipmentTemplateID: eq.ItemID, WhiteScroll: r.WhiteFlags == 2, LegendarySpirit: r.LegendarySpirit, CurseRoll: -1}
	result.SuccessRoll, err = randN()
	if err != nil {
		return result, err
	}
	if result.SuccessRoll < 0 || result.SuccessRoll >= 10000 {
		return domain.ScrollResult{}, domain.ErrInvalidInput
	}
	if result.SuccessRoll < scroll.Success*100 {
		result.Status = 1
	} else if scroll.Curse > 0 {
		result.CurseRoll, err = randN()
		if err != nil {
			return result, err
		}
		if result.CurseRoll < 0 || result.CurseRoll >= 10000 {
			return domain.ScrollResult{}, domain.ErrInvalidInput
		}
		if result.CurseRoll < scroll.Curse*100 {
			result.Status = 2
		}
	}
	// SQL wall clock can advance while WZ/entropy runs. A held shared row
	// lock does not freeze expires_at > clock_timestamp(). Recheck before spend.
	if err = guardLease(ctx, tx, r.Session.Lease); err != nil {
		return result, err
	}
	if err = guardItemExpiry(ctx, tx, eq.ExpiresAt); err != nil {
		return result, err
	}
	oldHP, oldMP := equipmentVitals(c.Equipment)
	if result.Status == 1 {
		if err = addScrollStats(&eq, scroll.Stats); err != nil {
			return result, err
		}
		eq.ScrollLevel++
		eq.UpgradeSlots--
	} else if !result.WhiteScroll {
		eq.UpgradeSlots--
	}
	if err = consumeScrollMaterial(ctx, tx, &result.Inventory, materialIndex); err != nil {
		return result, err
	}
	if result.WhiteScroll {
		for i, item := range result.Inventory.Character.Items {
			if item.InvType == 2 && item.TemplateID == 2340000 {
				if err = consumeScrollMaterial(ctx, tx, &result.Inventory, i); err != nil {
					return result, err
				}
				break
			}
		}
	}
	result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 3, InvType: 1, Slot: eq.Slot})
	if result.Status == 2 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1 AND character_id=$2`, eq.ID, c.ID); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_equipment_metadata WHERE equipment_id=$1`, eq.ID); err != nil {
			return result, err
		}
		result.Inventory.Character.Equipment = append(result.Inventory.Character.Equipment[:equipmentIndex], result.Inventory.Character.Equipment[equipmentIndex+1:]...)
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE asset_equipment SET str=$1,dex=$2,int_stat=$3,luk=$4,hp=$5,mp=$6,watk=$7,matk=$8,wdef=$9,mdef=$10,acc=$11,avoid=$12,speed=$13,jump=$14,upgrade_slots=$15 WHERE id=$16 AND character_id=$17`, eq.STR, eq.DEX, eq.INT, eq.LUK, eq.HP, eq.MP, eq.WATK, eq.MATK, eq.WDEF, eq.MDEF, eq.ACC, eq.AVOID, eq.Speed, eq.Jump, eq.UpgradeSlots, eq.ID, c.ID); err != nil {
			return result, err
		}
		result.Inventory.Character.Equipment[equipmentIndex] = eq
		result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 0, InvType: 1, Slot: eq.Slot, Equipment: &eq})
	}
	updated := &result.Inventory.Character
	if err = recomputeEquipmentVitals(updated, oldHP, oldMP); err != nil {
		return result, err
	}
	updated.HP = min(updated.HP, updated.MaxHP)
	updated.MP = min(updated.MP, updated.MaxMP)
	if err = commitInventoryState(ctx, tx, updated); err != nil {
		return result, err
	}
	result.CommittedRevision = updated.Revision
	plan.CommittedRevision, plan.Stats, plan.Success, plan.Curse, plan.SuccessRoll, plan.CurseRoll, plan.Status = updated.Revision, scroll.Stats, scroll.Success, scroll.Curse, result.SuccessRoll, result.CurseRoll, result.Status
	result.PlanJSON, result.PlanSHA256, err = domain.EncodeScrollPlan(plan)
	if err != nil {
		return result, err
	}
	if err = domain.ValidateScrollReceipt(r, result); err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_enhancement_commands(character_id,session_id,generation,client_tick,request_hash,result) VALUES($1,$2,$3,$4,$5,$6)`, c.ID, r.Session.SessionID, r.Session.Generation, int64(r.ClientTick), scrollRequestHash(r), raw); err != nil {
		return result, err
	}
	if err = commit(tx); err != nil {
		return result, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}
