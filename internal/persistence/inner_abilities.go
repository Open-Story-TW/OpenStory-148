package persistence

import (
	"context"
	"database/sql"

	"maple-go-server/internal/domain"
)

func readInnerAbilities(ctx context.Context, q questQuerier, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT p.honor_level,p.honor_exp,l.slot,l.skill_id,l.level,l.grade
		FROM game_inner_ability_progress p LEFT JOIN game_inner_abilities l USING(character_id)
		WHERE p.character_id=$1 ORDER BY l.slot`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slot, id, level, grade sql.NullInt64
		if err := rows.Scan(&c.HonorLevel, &c.HonorEXP, &slot, &id, &level, &grade); err != nil {
			return err
		}
		if !slot.Valid {
			continue
		}
		ability := domain.InnerAbility{ID: int(id.Int64), Level: int(level.Int64), Grade: int(grade.Int64)}
		if slot.Int64 < 1 || slot.Int64 > int64(len(c.Abilities)) || !ability.Valid() {
			return domain.ErrInvalidInput
		}
		c.Abilities[slot.Int64-1] = ability
	}
	return rows.Err()
}
