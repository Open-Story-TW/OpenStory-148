package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

var _ domain.ItemUseRepository = (*Store)(nil)

// UseItem retains the repository compatibility API. Live traffic uses the
// explicit Apply/Resolve result so uncertain commits never become a retry.
func (s *Store) UseItem(ctx context.Context, req domain.UseItemRequest) (domain.Character, error) {
	result, err := s.ApplyItemUse(ctx, req)
	return result.Character, err
}

func validItemUse(req domain.UseItemRequest) bool {
	v := req.Session
	return req.Slot >= 1 && req.Slot <= domain.MaxInventorySlots && req.TemplateID >= 2000000 && req.TemplateID < 3000000 &&
		v.CharacterID > 0 && v.AccountID > 0 && v.Generation > 0 && v.SessionID != "" && len(v.SessionID) <= 256 &&
		v.Character.ID == v.CharacterID && v.Character.AccountID == v.AccountID && v.Character.WorldID == v.Lease.WorldID &&
		v.ChannelID >= 1 && v.ChannelID <= 20 && v.Character.Revision > 0
}

// Same order as checkpoints: lease -> account -> ownership -> character.
// Resolve takes these same locks before checking absence: an in-flight Apply
// must finish COMMIT/ROLLBACK before its result can be declared absent.
func lockItemUseIdentity(ctx context.Context, tx *sql.Tx, req domain.UseItemRequest) (domain.Character, error) {
	s := req.Session
	if err := guardLease(ctx, tx, s.Lease); err != nil {
		return domain.Character{}, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR SHARE`, s.AccountID, s.SessionID, s.Generation, s.Lease.InstanceID, s.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}
	var channel int
	err = tx.QueryRowContext(ctx, `SELECT channel_id FROM ops_character_ownership WHERE character_id=$1 AND account_id=$2 AND session_id=$3 AND generation=$4 AND instance_id=$5 AND epoch=$6 FOR SHARE`, s.CharacterID, s.AccountID, s.SessionID, s.Generation, s.Lease.InstanceID, s.Lease.Epoch).Scan(&channel)
	if errors.Is(err, sql.ErrNoRows) || err == nil && channel != s.ChannelID {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err != nil {
		return domain.Character{}, err
	}
	var c domain.Character
	err = tx.QueryRowContext(ctx, `SELECT id,account_id,world_id,revision FROM game_characters WHERE id=$1 FOR UPDATE`, s.CharacterID).Scan(&c.ID, &c.AccountID, &c.WorldID, &c.Revision)
	if err != nil {
		return domain.Character{}, err
	}
	if c.AccountID != s.AccountID || c.WorldID != s.Lease.WorldID {
		return domain.Character{}, domain.ErrForbidden
	}
	return c, nil
}

func itemUseDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func validateItemUseReceipt(q domain.UseItemRequest, r domain.ItemUseResult) error {
	c := r.Character
	if r.Replayed || r.SessionID != q.Session.SessionID || r.Generation != q.Session.Generation || r.ChannelID != q.Session.ChannelID || r.ClientTick != q.ClientTick || r.Slot != q.Slot || r.TemplateID != q.TemplateID || r.ItemInstanceID <= 0 || r.QuantityAfter < 0 || r.QuantityAfter > 29999 ||
		r.CommittedRevision <= 0 || c.Revision != r.CommittedRevision || c.ID != q.Session.CharacterID || c.AccountID != q.Session.AccountID || c.WorldID != q.Session.Lease.WorldID || r.Slot > c.Capacity(2) {
		return domain.ErrForbidden
	}
	count := 0
	for _, item := range c.Items {
		if item.ID == r.ItemInstanceID || item.InvType == 2 && item.Slot == r.Slot {
			if item.ID != r.ItemInstanceID || item.InvType != 2 || item.Slot != r.Slot || item.TemplateID != r.TemplateID || item.Quantity != r.QuantityAfter || item.Quantity <= 0 {
				return domain.ErrForbidden
			}
			count++
		}
	}
	if r.QuantityAfter == 0 && count != 0 || r.QuantityAfter > 0 && count != 1 {
		return domain.ErrForbidden
	}
	v, err := content.CalculatePotionRecovery(content.Recovery{HP: r.FlatHP, MP: r.FlatMP, HPRate: r.HPRate, MPRate: r.MPRate}, content.PotionVitals{HP: r.HPBefore, MP: r.MPBefore, MaxHP: c.MaxHP, MaxMP: c.MaxMP}, content.PotionRecoveryModifiers{FixedHPPercent: r.FixedHPPercent}, domain.IsDemonJob(c.Job))
	if err != nil || v.HP != c.HP || v.MP != c.MP {
		return domain.ErrForbidden
	}
	return nil
}

func readItemUseReceipt(ctx context.Context, tx *sql.Tx, q domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
	var slot, template, qty, hp int
	var instance, revision int64
	var raw []byte
	var digest sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT slot,template_id,item_instance_id,quantity_after,hp_after,character_revision,result_bytes,result_sha256 FROM asset_item_uses WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4`, q.Session.CharacterID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick)).Scan(&slot, &template, &instance, &qty, &hp, &revision, &raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ItemUseResult{}, false, nil
	}
	if err != nil {
		return domain.ItemUseResult{}, false, err
	}
	if slot != q.Slot || template != q.TemplateID {
		return domain.ItemUseResult{}, true, domain.ErrForbidden
	}
	if raw == nil || !digest.Valid {
		return domain.ItemUseResult{}, true, domain.ErrLegacyItemUseReceipt
	}
	if len(raw) == 0 || len(raw) > 4<<20 || itemUseDigest(raw) != digest.String {
		return domain.ItemUseResult{}, true, domain.ErrItemUseReceiptInvalid
	}
	var result domain.ItemUseResult
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return domain.ItemUseResult{}, true, domain.ErrItemUseReceiptInvalid
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return domain.ItemUseResult{}, true, domain.ErrItemUseReceiptInvalid
	}
	if result.ItemInstanceID != instance || result.CommittedRevision != revision || result.QuantityAfter != qty || result.Character.HP != hp {
		return domain.ItemUseResult{}, true, domain.ErrItemUseReceiptInvalid
	}
	if err := validateItemUseReceipt(q, result); err != nil {
		return domain.ItemUseResult{}, true, domain.ErrItemUseReceiptInvalid
	}
	return result, true, nil
}

// ResolveItemUse is read-only, including when the current WZ is unavailable.
// It returns the immutable result, never re-heals, re-consumes or recalculates.
func (s *Store) ResolveItemUse(ctx context.Context, q domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
	if !validItemUse(q) {
		return domain.ItemUseResult{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ItemUseResult{}, false, err
	}
	defer tx.Rollback()
	c, err := lockItemUseIdentity(ctx, tx, q)
	if err != nil {
		return domain.ItemUseResult{}, false, err
	}
	result, found, err := readItemUseReceipt(ctx, tx, q)
	if err == nil && !found && c.Revision != q.Session.Character.Revision {
		return domain.ItemUseResult{}, false, domain.ErrStaleOwner
	}
	if err == nil && found && c.Revision != result.CommittedRevision {
		return domain.ItemUseResult{}, true, domain.ErrStaleOwner
	}
	return result, found, err
}

func (s *Store) ApplyItemUse(ctx context.Context, req domain.UseItemRequest) (domain.ItemUseResult, error) {
	return s.applyItemUse(ctx, req, (*sql.Tx).Commit)
}

// The commit seam permits a real PG lost-ack fixture: COMMIT succeeds but its
// transport reply is lost. Production always supplies the original Commit.
func (s *Store) applyItemUse(ctx context.Context, req domain.UseItemRequest, commit func(*sql.Tx) error) (domain.ItemUseResult, error) {
	if !validItemUse(req) {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	defer tx.Rollback()
	identity, err := lockItemUseIdentity(ctx, tx, req)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	prior, found, err := readItemUseReceipt(ctx, tx, req)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	if found {
		// Preserve historical audit, but do not replay an older aggregate/HP delta.
		prior.Character, err = readCharacterSQL(ctx, tx, identity.ID)
		prior.Replayed = true
		return prior, err
	}
	c, err := readCharacterSQL(ctx, tx, identity.ID)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	if c.Revision != req.Session.Character.Revision || c.MapID != req.Session.Character.MapID {
		return domain.ItemUseResult{}, domain.ErrStaleOwner
	}
	if req.Slot > c.Capacity(2) || c.MapID == 749040100 {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	hp, mp := req.Session.Character.HP, req.Session.Character.MP
	if hp <= 0 || hp > req.Session.Character.MaxHP || mp < 0 || mp > req.Session.Character.MaxMP {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	index := -1
	for i, item := range c.Items {
		if item.InvType == 2 && item.Slot == req.Slot && item.TemplateID == req.TemplateID {
			if index != -1 {
				return domain.ItemUseResult{}, domain.ErrForbidden
			}
			index = i
		}
	}
	if index < 0 {
		return domain.ItemUseResult{}, domain.ErrNotFound
	}
	item := c.Items[index]
	if item.ID <= 0 || item.Quantity <= 0 {
		return domain.ItemUseResult{}, domain.ErrForbidden
	}
	recovery, itemBuff, err := content.ConsumableDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), req.TemplateID)
	if err != nil {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return domain.ItemUseResult{}, err
	}
	if item.ExpiresAt > 0 && item.ExpiresAt <= now.UnixMilli() {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	if c.VitalsModel == 1 {
		c.MaxHP, c.MaxMP, err = content.DerivedVitals(c, now)
		if err != nil {
			return domain.ItemUseResult{}, domain.ErrForbidden
		}
	}
	modifiers := content.PotionRecoveryModifiers{}
	if recovery.HP > 0 {
		modifiers, err = skills.PotionRecoveryModifiersChecked(c, now)
		if err != nil {
			return domain.ItemUseResult{}, domain.ErrForbidden
		}
	}
	vitals, err := content.CalculatePotionRecovery(recovery, content.PotionVitals{HP: hp, MP: mp, MaxHP: c.MaxHP, MaxMP: c.MaxMP}, modifiers, domain.IsDemonJob(c.Job))
	if err != nil {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	if err = guardLease(ctx, tx, req.Session.Lease); err != nil {
		return domain.ItemUseResult{}, err
	}
	if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
		return domain.ItemUseResult{}, err
	}
	quantity := item.Quantity - 1
	if quantity == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID)
		c.Items = append(c.Items[:index], c.Items[index+1:]...)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, quantity, item.ID)
		c.Items[index].Quantity = quantity
		c.Items[index].Revision++
	}
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	c.HP, c.MP = vitals.HP, vitals.MP
	var buffs, replaced []domain.SkillBuff
	if itemBuff != nil {
		if buffs, replaced, err = applyItemBuff(ctx, tx, &c, req.TemplateID, itemBuff, now); err != nil {
			return domain.ItemUseResult{}, err
		}
	}
	c.Revision++
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET hp=$1,mp=$2,max_hp=$3,max_mp=$4,revision=$5 WHERE id=$6`, c.HP, c.MP, c.MaxHP, c.MaxMP, c.Revision, c.ID); err != nil {
		return domain.ItemUseResult{}, err
	}
	if err = syncCashLocations(ctx, tx, c); err != nil {
		return domain.ItemUseResult{}, err
	}
	result := domain.ItemUseResult{Character: c, SessionID: req.Session.SessionID, Generation: req.Session.Generation, ChannelID: req.Session.ChannelID, ClientTick: req.ClientTick, Slot: req.Slot, TemplateID: req.TemplateID, ItemInstanceID: item.ID, QuantityAfter: quantity, CommittedRevision: c.Revision, HPBefore: hp, MPBefore: mp, FlatHP: recovery.HP, FlatMP: recovery.MP, HPRate: recovery.HPRate, MPRate: recovery.MPRate, FixedHPPercent: modifiers.FixedHPPercent, Buffs: buffs, Replaced: replaced}
	if err = validateItemUseReceipt(req, result); err != nil {
		return domain.ItemUseResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	if len(raw) > 4<<20 {
		return domain.ItemUseResult{}, domain.ErrInvalidInput
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_item_uses(character_id,session_id,generation,client_tick,item_instance_id,template_id,slot,quantity_after,hp_after,character_revision,result_bytes,result_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, c.ID, req.Session.SessionID, req.Session.Generation, int64(req.ClientTick), item.ID, item.TemplateID, item.Slot, quantity, c.HP, c.Revision, raw, itemUseDigest(raw)); err != nil {
		return domain.ItemUseResult{}, err
	}
	roster, err := marshalRoster(c)
	if err != nil {
		return domain.ItemUseResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1,revision=$2 WHERE character_id=$3`, roster, c.Revision, c.ID); err != nil {
		return domain.ItemUseResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type,account_id,world_id) VALUES('roster.invalidate',$1,$2)`, c.AccountID, c.WorldID); err != nil {
		return domain.ItemUseResult{}, err
	}
	if err = commit(tx); err != nil {
		return domain.ItemUseResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

// ItemBuffSource is the buff source of a USE item: Java gives item effects
// the negative item ID, which the client echoes when the icon is cancelled.
func ItemBuffSource(templateID int) int { return -templateID }

// applyItemBuff stores the timed stats of a USE item as buff rows under
// ItemBuffSource, replacing any row with the same stat like a skill cast.
func applyItemBuff(ctx context.Context, tx *sql.Tx, c *domain.Character, templateID int, buff *content.ItemBuff, now time.Time) ([]domain.SkillBuff, []domain.SkillBuff, error) {
	stats := skills.StandardStats(buff.Values)
	if templateID == content.HorntailDisguiseItem && buff.Values["morph"] == 4 {
		stats = append(stats, skills.StatValue{Stat: "MORPH", Value: 4})
	}
	if v := buff.Values["inflation"]; v > 0 {
		stats = append(stats, skills.StatValue{Stat: "GIANT_POTION", Value: v})
	}
	if len(stats) == 0 {
		return nil, nil, domain.ErrInvalidInput
	}
	rows := make([]domain.SkillBuff, len(stats))
	for i, v := range stats {
		rows[i] = domain.SkillBuff{ID: ItemBuffSource(templateID), Level: 1, Stat: string(v.Stat), Value: v.Value, ExpiresAt: now.Add(buff.Duration)}
	}
	var r domain.SkillResult
	if err := replaceSkillBuffs(ctx, tx, c, &r, rows); err != nil {
		return nil, nil, err
	}
	return rows, r.Expired, nil
}
