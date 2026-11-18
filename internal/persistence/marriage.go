package persistence

import (
	"context"

	"maple-go-server/internal/domain"
)

func (s *Store) CharacterMarried(ctx context.Context, session domain.CharacterSession) (bool, error) {
	var married bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM game_marriage_members member
			JOIN game_marriage_members partner ON partner.marriage_id = member.marriage_id
				AND partner.position <> member.position
			JOIN game_characters actor ON actor.id = member.character_id
			JOIN game_characters spouse ON spouse.id = partner.character_id
				AND spouse.world_id = actor.world_id
			WHERE actor.id = $1 AND actor.account_id = $2 AND actor.world_id = $3
		)`, session.CharacterID, session.AccountID, session.Lease.WorldID).Scan(&married)
	return married, err
}
