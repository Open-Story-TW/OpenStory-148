package persistence

import (
	"context"
	"database/sql"
	"errors"
	"maple-go-server/internal/domain"
)

func readCharacterCards(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, accountID int64, worldID int) (domain.CharacterCards, error) {
	var cards domain.CharacterCards
	rows, err := q.QueryContext(ctx, `SELECT d.slot,c.id,c.level,c.job FROM game_character_cards d
JOIN game_characters c ON (c.account_id,c.world_id,c.id)=(d.account_id,d.world_id,d.character_id)
WHERE d.account_id=$1 AND d.world_id=$2 ORDER BY d.slot`, accountID, worldID)
	if err != nil {
		return cards, err
	}
	defer rows.Close()
	for rows.Next() {
		var slot int
		var card domain.CharacterCard
		if err = rows.Scan(&slot, &card.CharacterID, &card.Level, &card.Job); err != nil {
			return domain.CharacterCards{}, err
		}
		if slot >= 0 && slot < len(cards) && card.Rank() > 0 {
			cards[slot] = card
		}
	}
	return cards, rows.Err()
}

func (s *Store) LoadCharacterCards(ctx context.Context, accountID int64, worldID int) (domain.CharacterCards, error) {
	return readCharacterCards(ctx, s.db, accountID, worldID)
}

func (s *Store) SaveCharacterCards(ctx context.Context, owner domain.LoginSession, worldID int, ids domain.CharacterCardIDs) error {
	if owner.AccountID <= 0 || owner.Generation <= 0 || owner.SessionID == "" || worldID < 0 || worldID > 127 {
		return domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	err = tx.QueryRowContext(ctx, `UPDATE ops_account_sessions SET expires_at=clock_timestamp()+interval '6 minutes'
WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='login' AND expires_at>clock_timestamp()
RETURNING account_id`, owner.AccountID, owner.SessionID, owner.Generation).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	var cards domain.CharacterCards
	for slot, id := range ids {
		if id == 0 {
			continue
		}
		card := domain.CharacterCard{CharacterID: id}
		err = tx.QueryRowContext(ctx, `SELECT level,job FROM game_characters
WHERE id=$1 AND account_id=$2 AND world_id=$3 FOR SHARE`, id, accountID, worldID).Scan(&card.Level, &card.Job)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrForbidden
		}
		if err != nil {
			return err
		}
		cards[slot] = card
	}
	if err = domain.ValidateCharacterCards(cards); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM game_character_cards WHERE account_id=$1 AND world_id=$2`, accountID, worldID); err != nil {
		return err
	}
	for slot, id := range ids {
		if id == 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_character_cards(account_id,world_id,slot,character_id)
VALUES($1,$2,$3,$4)`, accountID, worldID, slot, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
