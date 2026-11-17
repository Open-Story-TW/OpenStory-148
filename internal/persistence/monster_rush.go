package persistence

import (
	"context"
	"strconv"
	"strings"

	"maple-go-server/internal/domain"
)

func (s *Store) MonsterRushSchedules(ctx context.Context, worldID int) (map[int]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, value FROM game_script_logs
		WHERE scope='world' AND owner_id=$1 AND world_id=$1 AND period='permanent'
		  AND name LIKE $2 AND value>0`, worldID, domain.MonsterRushSchedulePrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]int64)
	for rows.Next() {
		var name string
		var start int64
		if err := rows.Scan(&name, &start); err != nil {
			return nil, err
		}
		if channel, err := strconv.Atoi(strings.TrimPrefix(name, domain.MonsterRushSchedulePrefix)); err == nil && channel > 0 {
			result[channel] = start
		}
	}
	return result, rows.Err()
}
