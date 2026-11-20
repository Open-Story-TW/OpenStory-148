package persistence

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"reflect"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.PotentialRepository = (*Store)(nil)

const potentialCommandFamily = "potential-v1"

func validPotentialCommand(r domain.PotentialCommandRequest) bool {
	return (r.Action == domain.PotentialSeal || r.Action == domain.PotentialMagnify) &&
		r.SourceSlot >= 1 && r.SourceSlot <= domain.MaxInventorySlots && r.TargetSlot != 0 && r.TargetSlot >= -199 && r.TargetSlot <= domain.MaxInventorySlots &&
		(r.Action != domain.PotentialMagnify || !r.LegendarySpirit) && r.Session.ChannelID >= 1 && r.Session.ChannelID <= 20 &&
		r.Session.SessionID != "" && len(r.Session.SessionID) <= 256 && r.Session.Generation > 0 && r.Session.AccountID > 0 && r.Session.CharacterID > 0 &&
		r.Session.Lease.WorldID >= 0 && r.Session.Lease.InstanceID != "" && r.Session.Lease.Epoch > 0 &&
		r.Session.Character.ID == r.Session.CharacterID && r.Session.Character.AccountID == r.Session.AccountID && r.Session.Character.WorldID == r.Session.Lease.WorldID
}

func potentialRequestHash(r domain.PotentialCommandRequest) string {
	v := struct {
		Family          string
		Action          domain.PotentialAction
		Source, Target  int
		LegendarySpirit bool
	}{potentialCommandFamily, r.Action, r.SourceSlot, r.TargetSlot, r.LegendarySpirit}
	raw, _ := json.Marshal(v)
	return tokenHash(string(raw))
}

func potentialOperationID(r domain.PotentialCommandRequest) string {
	return "potential/" + tokenHash(fmt.Sprintf("%s/%d/%d/%d/%s/%d/%d/%s", potentialCommandFamily, r.Session.AccountID, r.Session.CharacterID,
		r.Session.Lease.WorldID, r.Session.SessionID, r.Session.Generation, r.ClientTick, potentialRequestHash(r)))
}

func potentialRandN(bound int) (int, error) {
	if bound < 1 {
		return 0, domain.ErrInvalidInput
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(bound)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

// Same lease -> account -> ownership -> character lock order as inventory,
// without readCharacterSQL: that loader recomputes WZ-dependent derived vitals.
// Resolution must still work when a committed plan's WZ is unavailable later.
func lockPotentialIdentity(ctx context.Context, tx *sql.Tx, r domain.PotentialCommandRequest) (domain.Character, error) {
	if err := guardLease(ctx, tx, r.Session.Lease); err != nil {
		return domain.Character{}, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR SHARE`,
		r.Session.AccountID, r.Session.SessionID, r.Session.Generation, r.Session.Lease.InstanceID, r.Session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}
	var channel int
	err = tx.QueryRowContext(ctx, `SELECT channel_id FROM ops_character_ownership WHERE character_id=$1 AND account_id=$2 AND session_id=$3 AND generation=$4 AND instance_id=$5 AND epoch=$6 FOR SHARE`,
		r.Session.CharacterID, r.Session.AccountID, r.Session.SessionID, r.Session.Generation, r.Session.Lease.InstanceID, r.Session.Lease.Epoch).Scan(&channel)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}
	if channel != r.Session.ChannelID {
		return domain.Character{}, domain.ErrStaleOwner
	}
	var c domain.Character
	err = tx.QueryRowContext(ctx, `SELECT id,account_id,world_id,revision FROM game_characters WHERE id=$1 FOR UPDATE`, r.Session.CharacterID).
		Scan(&c.ID, &c.AccountID, &c.WorldID, &c.Revision)
	if err != nil {
		return domain.Character{}, err
	}
	if c.AccountID != r.Session.AccountID || c.WorldID != r.Session.Lease.WorldID {
		return domain.Character{}, domain.ErrForbidden
	}
	return c, nil
}

func potentialCommandState(c domain.Character, r domain.PotentialCommandRequest) (int, int, error) {
	if c.Revision != r.Session.Character.Revision {
		return -1, -1, domain.ErrStaleOwner
	}
	if c.VitalsModel != 1 {
		// Do not sell a new potential to an ambiguous legacy base, including
		// bag targets which cannot later be equipped with known HP/MP rules.
		return -1, -1, domain.ErrForbidden
	}
	if r.SourceSlot > c.Capacity(2) || r.TargetSlot > c.Capacity(1) {
		return -1, -1, domain.ErrInvalidInput
	}
	// HP/MP come from the trusted world session's current runtime snapshot,
	// rather than an older periodic SQL checkpoint. No client HP is accepted.
	if r.Session.Character.HP <= 0 || r.Session.Character.HP > c.MaxHP || r.Session.Character.MP < 0 || r.Session.Character.MP > c.MaxMP {
		return -1, -1, domain.ErrForbidden
	}
	if r.Action == domain.PotentialSeal {
		if r.TargetSlot > 0 {
			if !r.LegendarySpirit || !learnedLegendarySpirit(c) {
				return -1, -1, domain.ErrForbidden
			}
		} else if r.LegendarySpirit {
			return -1, -1, domain.ErrInvalidInput
		}
	}
	material, equipment := -1, -1
	for i, item := range c.Items {
		if item.InvType == 2 && item.Slot == r.SourceSlot {
			material = i
			break
		}
	}
	for i, item := range c.Equipment {
		if item.Slot == r.TargetSlot {
			equipment = i
			break
		}
	}
	if material < 0 || equipment < 0 {
		return -1, -1, domain.ErrNotFound
	}
	item := c.Items[material]
	if item.ID < 1 || c.Equipment[equipment].ID < 1 || item.Quantity < 1 {
		return -1, -1, domain.ErrInsufficient
	}
	if item.CashID != 0 {
		return -1, -1, domain.ErrForbidden // cash-serial materials are a separate increment.
	}
	if (r.Action == domain.PotentialSeal && item.TemplateID/100 != 20494 && item.TemplateID/100 != 20497) ||
		(r.Action == domain.PotentialMagnify && (item.TemplateID < 2460000 || item.TemplateID > 2460003)) {
		return -1, -1, domain.ErrInvalidInput // excludes CASH5534000 and trait source127.
	}
	return material, equipment, nil
}

func potentialFeeBeforeRoll(target content.PotentialTarget, action domain.PotentialAction) (int64, error) {
	if action == domain.PotentialSeal {
		return 0, nil
	}
	// Mirror only the audited hidden-tier fee calculation, never a random draw.
	// The pure engine subsequently checks the complete state/candidate rules.
	tier := int(int16(target.Potential.Options[0]))
	if tier < 0 {
		tier = -tier
	}
	if tier < 17 || tier > 20 {
		tier = 17
	}
	fee, err := content.PotentialMagnifyFee(target.RequiredLevel, tier)
	return int64(fee), err
}

func guardPotentialSpirit(ctx context.Context, tx *sql.Tx, r domain.PotentialCommandRequest, c domain.Character) error {
	if r.Action != domain.PotentialSeal || r.TargetSlot < 0 {
		return nil
	}
	return guardLearnedLegendarySpirit(ctx, tx, c)
}

func potentialPlanDigest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func validatePotentialReceipt(r domain.PotentialCommandRequest, value domain.PotentialCommandResult) error {
	if value.Action != r.Action || value.SourceSlot != r.SourceSlot || value.TargetSlot != r.TargetSlot || value.LegendarySpirit != r.LegendarySpirit ||
		value.OperationID != potentialOperationID(r) || value.Status < 0 || value.Status > 1 || value.ToolInstanceID < 1 || value.EquipmentID < 1 ||
		value.MesoFee < 0 || value.MesoFee > maxMesos || value.CommittedRevision != value.Inventory.Character.Revision || value.CommittedRevision < 1 ||
		value.Inventory.Character.ID != r.Session.CharacterID || value.Inventory.Character.AccountID != r.Session.AccountID || value.Inventory.Character.WorldID != r.Session.Lease.WorldID ||
		len(value.PlanJSON) == 0 || len(value.PlanJSON) > 4<<20 || value.PlanSHA256 != potentialPlanDigest(value.PlanJSON) || value.Normalization == "" ||
		value.Replayed || value.Inventory.Replayed || value.Inventory.Drop != nil {
		return domain.ErrForbidden
	}
	var plan content.PotentialPlan
	d := json.NewDecoder(bytes.NewReader(value.PlanJSON))
	d.DisallowUnknownFields()
	if err := d.Decode(&plan); err != nil {
		return domain.ErrForbidden
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return domain.ErrForbidden
	}
	if plan.PolicyVersion != content.PotentialPolicyVersion || plan.Operation != string(r.Action) || plan.ToolID != value.ToolID || plan.ToolCount != 1 ||
		int64(plan.MesoFee) != value.MesoFee || (value.Status == 1) != plan.ScrollSuccess || plan.Before != value.LogicalPolicyBefore ||
		plan.Target.Potential != value.LogicalPolicyBefore || plan.Target.ItemID != value.EquipmentTemplateID || !plan.After.Present ||
		plan.After.Flags != value.LogicalPolicyBefore.Flags || value.BeforeStored != value.EquipmentBefore.Potential ||
		value.EquipmentBefore.ID != value.EquipmentID || value.EquipmentBefore.ItemID != value.EquipmentTemplateID || value.EquipmentBefore.Slot != value.TargetSlot {
		return domain.ErrForbidden
	}
	if decoded, err := hex.DecodeString(plan.WZSHA256); err != nil || len(decoded) != sha256.Size {
		return domain.ErrForbidden
	}
	// Reconstruct the captured facts, not today's mutable WZ files. All five
	// represented-or-zero level values are already frozen in the plan. The
	// shared normalizer verifies the three preimages without guessing sentinels
	// here or needing a catalog during read-only outcome resolution.
	facts := content.Item{ID: plan.Target.ItemID, InvType: 1, ReqLevel: plan.Target.RequiredLevel, Cash: plan.Target.Cash, FixedPotentialOptionCount: 5}
	for i, level := range plan.Target.OptionLevelOverrides {
		facts.FixedPotentialOptions[i].Level = level
	}
	view, err := content.PotentialOperationTarget(value.EquipmentBefore, facts)
	if err != nil || view.PolicyTarget != plan.Target || view.NativeBefore != value.NativeBefore || view.Normalization != value.Normalization || plan.Target.Cash {
		return domain.ErrForbidden
	}
	expected := value.EquipmentBefore
	expected.Potential = plan.After
	matches := 0
	for _, e := range value.Inventory.Character.Equipment {
		if e.Slot == value.TargetSlot && e.ID != value.EquipmentID {
			return domain.ErrForbidden
		}
		if e.ID == value.EquipmentID {
			if e != expected {
				return domain.ErrForbidden
			}
			matches++
		}
	}
	if matches != 1 {
		return domain.ErrForbidden
	}
	// Bind the published batch to its committed aggregate, not just the plan.
	// Remaining0 means the source was exhausted; otherwise its unique row
	// gives the exact quantity after consumption. The world additionally has
	// the original preimage and proves that this was precisely one material.
	remaining, sourceMatches, slotMatches := 0, 0, 0
	for _, item := range value.Inventory.Character.Items {
		if item.InvType == 2 && item.Slot == value.SourceSlot {
			slotMatches++
			if item.ID != value.ToolInstanceID {
				return domain.ErrForbidden
			}
		}
		if item.ID == value.ToolInstanceID {
			sourceMatches++
			if item.InvType != 2 || item.Slot != value.SourceSlot || item.TemplateID != value.ToolID || item.CashID != 0 || item.Quantity < 1 || item.Quantity >= 30000 {
				return domain.ErrForbidden
			}
			remaining = item.Quantity
		}
	}
	if sourceMatches > 1 || sourceMatches != slotMatches {
		return domain.ErrForbidden
	}
	source := domain.InventoryChange{Mode: 3, InvType: 2, Slot: value.SourceSlot}
	if remaining > 0 {
		source.Mode, source.Quantity = 1, remaining
	}
	canonical := []domain.InventoryChange{source, {Mode: 3, InvType: 1, Slot: value.TargetSlot}, {Mode: 0, InvType: 1, Slot: value.TargetSlot, Equipment: &expected}}
	if !reflect.DeepEqual(value.Inventory.Changes, canonical) {
		return domain.ErrForbidden
	}
	return nil
}

func readPotentialReceipt(ctx context.Context, tx *sql.Tx, r domain.PotentialCommandRequest, current domain.Character, exactRevision bool) (domain.PotentialCommandResult, bool, error) {
	var hash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_enhancement_commands WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4`, current.ID,
		r.Session.SessionID, r.Session.Generation, int64(r.ClientTick)).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PotentialCommandResult{}, false, nil
	}
	if err != nil {
		return domain.PotentialCommandResult{}, false, err
	}
	if hash != potentialRequestHash(r) {
		return domain.PotentialCommandResult{}, true, domain.ErrForbidden
	}
	var result domain.PotentialCommandResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.PotentialCommandResult{}, true, err
	}
	if err := validatePotentialReceipt(r, result); err != nil {
		return domain.PotentialCommandResult{}, true, err
	}
	if exactRevision && result.CommittedRevision != current.Revision {
		return domain.PotentialCommandResult{}, true, domain.ErrStaleOwner
	}
	result.Inventory.Replayed = true
	result.Replayed = true
	if !exactRevision {
		// A normal repeated action must never republish an old delta. Resolve,
		// unlike Apply replay, retains it only at the exact committed revision.
		result.Inventory.Changes = nil
	}
	return result, true, nil
}

func (s *Store) ResolvePotential(ctx context.Context, r domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error) {
	if !validPotentialCommand(r) {
		return domain.PotentialCommandResult{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PotentialCommandResult{}, false, err
	}
	defer tx.Rollback()
	current, err := lockPotentialIdentity(ctx, tx, r)
	if err != nil {
		return domain.PotentialCommandResult{}, false, err
	}
	// The same character lock waits for the original transaction to finish.
	// READ COMMITTED now sees its commit or proven absence. No WZ/RNG needed.
	return readPotentialReceipt(ctx, tx, r, current, true)
}

func (s *Store) ApplyPotential(ctx context.Context, r domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
	return s.applyPotential(ctx, r, potentialRandN, (*sql.Tx).Commit)
}

// RNG/commit injection is local to this call, never a mutable process-global
// testing switch. The public path always uses crypto/rand and real SQL Commit.
func (s *Store) applyPotential(ctx context.Context, r domain.PotentialCommandRequest, randN content.PotentialRandN, commit func(*sql.Tx) error) (domain.PotentialCommandResult, error) {
	if !validPotentialCommand(r) || randN == nil || commit == nil {
		return domain.PotentialCommandResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	defer tx.Rollback()
	c, err := lockPotentialIdentity(ctx, tx, r)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	prior, found, err := readPotentialReceipt(ctx, tx, r, c, false)
	if found || err != nil {
		if found && err == nil {
			prior.Inventory.Character, err = readCharacterSQL(ctx, tx, c.ID)
		}
		return prior, err
	}
	c, err = readCharacterSQL(ctx, tx, c.ID)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	materialIndex, equipmentIndex, err := potentialCommandState(c, r)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := guardPotentialSpirit(ctx, tx, r, c); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	material, equipment := c.Items[materialIndex], c.Equipment[equipmentIndex]
	if err := guardItemExpiry(ctx, tx, material.ExpiresAt); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := guardItemExpiry(ctx, tx, equipment.ExpiresAt); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	tool, err := inventoryDefinition(material.TemplateID)
	if err != nil || tool.InvType != 2 || tool.Cash {
		return domain.PotentialCommandResult{}, domain.ErrForbidden
	}
	item, err := inventoryDefinition(equipment.ItemID)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	view, err := content.PotentialOperationTarget(equipment, item)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if view.PolicyTarget.Cash {
		return domain.PotentialCommandResult{}, domain.ErrForbidden
	}
	fee, err := potentialFeeBeforeRoll(view.PolicyTarget, r.Action)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if c.Mesos < fee || c.Mesos > domain.MaxMoney() {
		return domain.PotentialCommandResult{}, domain.ErrInsufficient
	}
	catalog, err := content.LoadPotentialCatalog(os.Getenv("MAPLEGO_WZ_ROOT"))
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	var plan content.PotentialPlan
	if r.Action == domain.PotentialSeal {
		plan, err = content.SealPotential(catalog, content.PotentialSealRequest{Target: view.PolicyTarget, ScrollID: material.TemplateID}, content.DefaultPotentialPolicy(), randN)
	} else {
		plan, err = content.MagnifyPotential(catalog, content.PotentialMagnifyRequest{Target: view.PolicyTarget, MagnifierID: material.TemplateID}, content.DefaultPotentialPolicy(), randN)
	}
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if int64(plan.MesoFee) != fee || plan.ToolCount != 1 {
		return domain.PotentialCommandResult{}, domain.ErrForbidden
	}
	// Recheck real SQL time after bounded entropy/WZ work and before spending.
	if err := guardLease(ctx, tx, r.Session.Lease); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := guardPotentialSpirit(ctx, tx, r, c); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := guardItemExpiry(ctx, tx, material.ExpiresAt); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := guardItemExpiry(ctx, tx, equipment.ExpiresAt); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	result := domain.PotentialCommandResult{Inventory: domain.InventoryResult{Character: c}, Action: r.Action, OperationID: potentialOperationID(r),
		SourceSlot: r.SourceSlot, TargetSlot: r.TargetSlot, ToolID: material.TemplateID, ToolInstanceID: material.ID, EquipmentID: equipment.ID,
		EquipmentTemplateID: equipment.ItemID, LegendarySpirit: r.LegendarySpirit, MesoFee: fee, EquipmentBefore: view.EquipmentBefore,
		BeforeStored: equipment.Potential, NativeBefore: view.NativeBefore, LogicalPolicyBefore: view.PolicyTarget.Potential, Normalization: view.Normalization}
	if plan.ScrollSuccess {
		result.Status = 1
	}
	result.PlanJSON, err = json.Marshal(plan)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	result.PlanSHA256 = potentialPlanDigest(result.PlanJSON)
	if err := consumeScrollMaterial(ctx, tx, &result.Inventory, materialIndex); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	equipment.Potential = plan.After
	result.Inventory.Character.Equipment[equipmentIndex] = equipment
	result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 3, InvType: 1, Slot: equipment.Slot}, domain.InventoryChange{Mode: 0, InvType: 1, Slot: equipment.Slot, Equipment: &equipment})
	result.Inventory.Character.Mesos -= fee
	if fee != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'potential_magnify')`,
			result.OperationID, c.ID, c.AccountID, c.WorldID, -fee, result.Inventory.Character.Mesos); err != nil {
			return domain.PotentialCommandResult{}, err
		}
	}
	// Metadata, one material, mesos, the snapshot and the receipt are all in
	// this transaction. Shared commitInventoryState derives numeric vitals;
	// checked combat consumers and remaining proc effects have separate gates.
	if err := commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	result.CommittedRevision = result.Inventory.Character.Revision
	if err := validatePotentialReceipt(r, result); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_enhancement_commands(character_id,session_id,generation,client_tick,request_hash,result) VALUES($1,$2,$3,$4,$5,$6)`,
		c.ID, r.Session.SessionID, r.Session.Generation, int64(r.ClientTick), potentialRequestHash(r), raw); err != nil {
		return domain.PotentialCommandResult{}, err
	}
	if err := commit(tx); err != nil {
		return domain.PotentialCommandResult{}, fmt.Errorf("%w: %v", domain.ErrPotentialOutcomeUnknown, err)
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}
