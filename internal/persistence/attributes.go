package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"maple-go-server/internal/domain"
)

var _ domain.AttributeRepository = (*Store)(nil)

// The retained Java configuration's default 能力值限制 is 2000, below the
// native signed-short bound. Long stats lifts it to the wire contract's limit.
// HP/MP allocation has separate counters/formulas and is deliberately not
// treated as one of these four base attributes.
func baseAttributeLimit() int {
	if domain.LongStatsEnabled() {
		return domain.MaxStat()
	}
	return 2000
}

func (s *Store) AllocateAttribute(ctx context.Context, request domain.AttributeRequest) (domain.AttributeResult, error) {
	allocations := request.Allocations
	if len(allocations) == 0 {
		allocations = []domain.AttributeAllocation{{Stat: request.Stat, Amount: 1}}
	} else if request.Stat != 0 || len(allocations) > 4 {
		return domain.AttributeResult{}, domain.ErrInvalidInput
	}
	var total int
	for _, allocation := range allocations {
		if allocation.Stat != 0x40 && allocation.Stat != 0x80 && allocation.Stat != 0x100 && allocation.Stat != 0x200 || allocation.Amount < 0 || allocation.Amount > 32767 {
			return domain.AttributeResult{}, domain.ErrInvalidInput
		}
		total += int(allocation.Amount)
	}
	if total < 1 || total > 32767 {
		return domain.AttributeResult{}, domain.ErrInvalidInput
	}
	// Retain [] for legacy single-point receipts and the complete ordered
	// batch for automatic AP. Both formats share the same unique tick key.
	receiptAllocations := request.Allocations
	if receiptAllocations == nil {
		receiptAllocations = []domain.AttributeAllocation{}
	}
	payload, err := json.Marshal(receiptAllocations)
	if err != nil {
		return domain.AttributeResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AttributeResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, request.Session)
	if err != nil {
		return domain.AttributeResult{}, err
	}
	var oldMask uint64
	var oldPayload []byte
	err = tx.QueryRowContext(ctx, `SELECT stat_mask,allocations FROM asset_attribute_commands WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4`, c.ID, request.Session.SessionID, request.Session.Generation, request.ClientTick).Scan(&oldMask, &oldPayload)
	if err == nil {
		var oldAllocations []domain.AttributeAllocation
		if json.Unmarshal(oldPayload, &oldAllocations) != nil || oldMask != request.Stat || !slices.Equal(oldAllocations, receiptAllocations) {
			return domain.AttributeResult{}, domain.ErrForbidden
		}
		return domain.AttributeResult{Character: c, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.AttributeResult{}, err
	}
	if err = carrySkillVitals(&c, request.Session); err != nil {
		return domain.AttributeResult{}, err
	}
	if c.AP < total || c.HP <= 0 {
		return domain.AttributeResult{}, domain.ErrInsufficient
	}
	attributes := map[uint64]*int{0x40: &c.STR, 0x80: &c.DEX, 0x100: &c.INT, 0x200: &c.LUK}
	for _, allocation := range allocations {
		attribute := attributes[allocation.Stat]
		if allocation.Amount > 0 && *attribute+int(allocation.Amount) > baseAttributeLimit() {
			return domain.AttributeResult{}, domain.ErrForbidden
		}
		*attribute += int(allocation.Amount)
	}
	c.AP -= total
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET str=$1,dex=$2,int_stat=$3,luk=$4,ap=$5 WHERE id=$6`, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.ID); err != nil {
		return domain.AttributeResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return domain.AttributeResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_attribute_commands(character_id,session_id,generation,client_tick,stat_mask,allocations) VALUES($1,$2,$3,$4,$5,$6)`, c.ID, request.Session.SessionID, request.Session.Generation, request.ClientTick, request.Stat, payload); err != nil {
		return domain.AttributeResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.AttributeResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return domain.AttributeResult{Character: c}, nil
}
