package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"maple-go-server/internal/domain"
)

var _ domain.ScriptQuestRepository = (*Store)(nil)

func validScriptQuestID(id int) bool { return id > 0 && id <= 2147483647 }

func (s *Store) ReadScriptQuestData(ctx context.Context, session domain.CharacterSession, id int) (domain.ScriptQuestData, error) {
	value := domain.ScriptQuestData{QuestID: id}
	if !validScriptQuestID(id) {
		return value, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, err
	}
	defer tx.Rollback()
	if _, err = lockInventoryCharacter(ctx, tx, session); err != nil {
		return value, err
	}
	err = tx.QueryRowContext(ctx, `SELECT data,revision FROM game_script_quest_records WHERE character_id=$1 AND quest_id=$2`, session.CharacterID, id).Scan(&value.Data, &value.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func applyScriptQuestData(ctx context.Context, tx *sql.Tx, characterID int64, changes []domain.ScriptQuestChange) ([]domain.ScriptQuestData, error) {
	ordered := append([]domain.ScriptQuestChange(nil), changes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QuestID < ordered[j].QuestID })
	var out []domain.ScriptQuestData
	last := 0
	for _, change := range ordered {
		if !validScriptQuestID(change.QuestID) || change.QuestID == last || !utf8.ValidString(change.Data) || len(change.Data) > 4096 || strings.ContainsRune(change.Data, 0) {
			return nil, domain.ErrInvalidInput
		}
		if change.Expected != nil && (!utf8.ValidString(*change.Expected) || len(*change.Expected) > 4096 || strings.ContainsRune(*change.Expected, 0)) {
			return nil, domain.ErrInvalidInput
		}
		last = change.QuestID
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_script_quest_records(character_id,quest_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, characterID, change.QuestID); err != nil {
			return nil, err
		}
		value := domain.ScriptQuestData{QuestID: change.QuestID}
		if err := tx.QueryRowContext(ctx, `SELECT data,revision FROM game_script_quest_records WHERE character_id=$1 AND quest_id=$2 FOR UPDATE`, characterID, change.QuestID).Scan(&value.Data, &value.Revision); err != nil {
			return nil, err
		}
		if change.Expected != nil && value.Data != *change.Expected {
			return nil, domain.ErrAlreadyExists
		}
		value.Data = change.Data
		if err := tx.QueryRowContext(ctx, `UPDATE game_script_quest_records SET data=$3,revision=revision+1,updated_at=clock_timestamp() WHERE character_id=$1 AND quest_id=$2 RETURNING revision`, characterID, change.QuestID, change.Data).Scan(&value.Revision); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
