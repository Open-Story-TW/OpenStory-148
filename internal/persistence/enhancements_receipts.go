package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"maple-go-server/internal/domain"
)

func readScrollReceipt(ctx context.Context, tx *sql.Tx, q domain.ScrollRequest, current domain.Character, exact bool) (domain.ScrollResult, bool, error) {
	var hash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_enhancement_commands WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4`, q.Session.CharacterID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick)).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScrollResult{}, false, nil
	}
	if err != nil {
		return domain.ScrollResult{}, false, err
	}
	if hash != scrollRequestHash(q) {
		return domain.ScrollResult{}, true, domain.ErrForbidden
	}
	var v domain.ScrollResult
	if err = json.Unmarshal(raw, &v); err != nil {
		return v, true, fmt.Errorf("%w: invalid committed receipt: %v", domain.ErrScrollOutcomeUnknown, err)
	}
	if err = domain.ValidateScrollReceipt(q, v); err != nil {
		return v, true, fmt.Errorf("%w: invalid committed receipt: %v", domain.ErrScrollOutcomeUnknown, err)
	}
	if v.CommittedRevision == 0 {
		v.CommittedRevision = v.Inventory.Character.Revision
	} // validated legacy snapshot, never a guessed zero
	if exact && current.Revision != v.CommittedRevision {
		return domain.ScrollResult{}, true, domain.ErrStaleOwner
	}
	v.Replayed, v.Inventory.Replayed = true, true
	if !exact {
		v.Inventory.Character = current
		v.Inventory.Changes = nil
	}
	return v, true, nil
}

func (s *Store) ResolveScroll(ctx context.Context, q domain.ScrollRequest) (domain.ScrollResult, bool, error) {
	if q.SourceSlot < 1 || q.SourceSlot > domain.MaxInventorySlots || q.TargetSlot == 0 || q.TargetSlot < -199 || q.TargetSlot > domain.MaxInventorySlots || q.WhiteFlags != 0 && q.WhiteFlags != 2 {
		return domain.ScrollResult{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ScrollResult{}, false, err
	}
	defer tx.Rollback()
	// Reuse only the shared identity lock order: lease, session, ownership,
	// character. It neither loads WZ nor recomputes vitals. The character lock
	// waits for the original transaction before READ COMMITTED checks receipt.
	c, err := lockPotentialIdentity(ctx, tx, domain.PotentialCommandRequest{Session: q.Session})
	if err != nil {
		return domain.ScrollResult{}, false, err
	}
	return readScrollReceipt(ctx, tx, q, c, true)
}
