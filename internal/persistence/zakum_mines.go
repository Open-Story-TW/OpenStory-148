package persistence

import (
	"context"
	"fmt"
	"slices"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func (s *Store) OfferZakumMineKeys(ctx context.Context, session domain.CharacterSession, keys domain.ItemDrop, x, y int) (domain.ScriptExchangeResult, error) {
	if !content.ZakumMineMap(keys.MapID) || keys.MapID != session.Character.MapID ||
		keys.WorldID != session.Lease.WorldID || keys.ChannelID != session.ChannelID || keys.OwnerID != session.CharacterID ||
		keys.TemplateID != content.ZakumMineKey || keys.Quantity != 7 || !keys.PlayerDrop {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	defer tx.Rollback()
	result, err := executeScriptExchangeTx(ctx, tx, domain.ScriptExchange{
		Session: session, OperationID: fmt.Sprintf("zakum-mine-offer-%d", keys.ObjectID),
		Drops: []domain.ScriptDrop{{TemplateID: content.ZakumRoughOre, Quantity: 1, X: x, Y: y}},
	})
	if err != nil {
		return result, err
	}
	if !result.Replayed {
		applied, err := consumeEventDrop(ctx, tx, keys)
		if err != nil {
			return result, err
		}
		if !applied {
			return result, domain.ErrStaleOwner
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) CompleteZakumMine(ctx context.Context, group []domain.ScriptExchange) ([]domain.ScriptExchangeResult, error) {
	if len(group) == 0 || len(group) > 6 {
		return nil, domain.ErrInvalidInput
	}
	requests := make([]domain.ScriptExchange, len(group))
	leader := false
	bonus := group[0].EXP == 20000
	for i, exchange := range group {
		if (!bonus && exchange.EXP != 12000) || (bonus && exchange.EXP != 20000) ||
			!content.ZakumMineMap(exchange.Session.Character.MapID) {
			return nil, domain.ErrInvalidInput
		}
		expected := []domain.ScriptItemDelta{{TemplateID: content.ZakumFireOre, Quantity: 1}}
		if bonus {
			expected = append(expected, domain.ScriptItemDelta{TemplateID: 2030007, Quantity: 5})
		}
		if len(exchange.Items) > len(expected) {
			if leader || exchange.Session.Character.MapID != content.ZakumMineMain {
				return nil, domain.ErrInvalidInput
			}
			leader = true
			expected = append(expected, domain.ScriptItemDelta{TemplateID: content.ZakumRoughOre, Quantity: -1})
			if bonus {
				expected = append(expected, domain.ScriptItemDelta{TemplateID: content.ZakumMineDocument, Quantity: -30})
			}
		}
		if !slices.Equal(expected, exchange.Items) {
			return nil, domain.ErrInvalidInput
		}
		requests[i] = domain.ScriptExchange{Session: exchange.Session, OperationID: exchange.OperationID, Items: expected, EXP: exchange.EXP}
	}
	if !leader {
		return nil, domain.ErrInvalidInput
	}
	return s.executeEventExchanges(ctx, requests)
}

func (s *Store) ExpireZakumMineDrops(ctx context.Context, world, channel int, maps []int) error {
	if channel < 1 || channel > 20 || len(maps) != 30 {
		return domain.ErrInvalidInput
	}
	for _, mapID := range maps {
		if !content.ZakumMineMap(mapID) {
			return domain.ErrInvalidInput
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE asset_item_drops SET expired_at=clock_timestamp()
	    WHERE world_id=$1 AND channel_id=$2 AND map_id=ANY($3::integer[])
	      AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, world, channel, maps)
	return err
}
