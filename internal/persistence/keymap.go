package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"maple-go-server/internal/domain"
)

var _ domain.KeymapRepository = (*Store)(nil)

func readKeymap(ctx context.Context, tx *sql.Tx, id int64) (domain.Keymap, error) {
	var raw []byte
	var result domain.Keymap
	err := tx.QueryRowContext(ctx, `SELECT bindings FROM game_character_keymaps WHERE character_id=$1`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultKeymap(), nil
	}
	if err != nil {
		return result, err
	}
	var bindings []domain.KeyBinding
	if err = json.Unmarshal(raw, &bindings); err != nil {
		return result, err
	}
	if len(bindings) != domain.KeymapSlots {
		return result, domain.ErrInvalidInput
	}
	for i, b := range bindings {
		if !domain.ValidKeyBinding(b) {
			return result, domain.ErrInvalidInput
		}
		result[i] = b
	}
	return result, nil
}

func (s *Store) ReadKeymap(ctx context.Context, session domain.CharacterSession) (domain.Keymap, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Keymap{}, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return domain.Keymap{}, err
	}
	return readKeymap(ctx, tx, session.CharacterID)
}

func (s *Store) UpdateKeymap(ctx context.Context, session domain.CharacterSession, changes []domain.KeyChange) error {
	if len(changes) > domain.KeymapSlots {
		return domain.ErrInvalidInput
	}
	seen := map[int]bool{}
	for _, v := range changes {
		if v.Key < 0 || v.Key >= domain.KeymapSlots || seen[v.Key] || !domain.ValidKeyBinding(v.Binding) {
			return domain.ErrInvalidInput
		}
		seen[v.Key] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, session.CharacterID).Scan(&id); err != nil {
		return err
	}
	mapping, err := readKeymap(ctx, tx, id)
	if err != nil {
		return err
	}
	prior := mapping
	for _, v := range changes {
		mapping[v.Key] = v.Binding
	}
	if mapping == prior {
		return nil
	}
	raw, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_character_keymaps(character_id,bindings) VALUES($1,$2) ON CONFLICT(character_id) DO UPDATE SET bindings=$2,revision=game_character_keymaps.revision+1,updated_at=clock_timestamp()`, id, raw); err != nil {
		return err
	}
	return tx.Commit()
}
