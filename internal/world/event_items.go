package world

import (
	"context"
	"sort"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// exchangeEventItems takes the group's persistence locks in character order,
// just as party combat does, and updates the live inventories after commit.
// A zero map ID is reserved for refunding a failed admission.
func (s *Server) exchangeEventItems(ctx context.Context, inst *eventInstance, group []eventOccupant, mapID int, action string, item, quantity int) error {
	repo, ok := s.repo.(domain.EventItemRepository)
	if !ok {
		return domain.ErrForbidden
	}
	members := append([]eventOccupant(nil), group...)
	sort.Slice(members, func(i, j int) bool {
		return partySession(members[i].live).CharacterID < partySession(members[j].live).CharacterID
	})

	return func() error {
		for _, m := range members {
			m.live.persistMu.Lock()
			defer m.live.persistMu.Unlock()
		}
		requests := make([]domain.ScriptExchange, 0, len(members))
		for _, m := range members {
			session := s.skillSession(m.live)
			if mapID != 0 && (session.ChannelID != inst.channel || session.Character.MapID != mapID ||
				session.Character.HP <= 0 || s.eventOf(session.CharacterID) != inst) {
				return domain.ErrStaleOwner
			}
			requests = append(requests, domain.ScriptExchange{
				Session: session, OperationID: "event-" + inst.id + "-" + action,
				Items: []domain.ScriptItemDelta{{TemplateID: item, Quantity: quantity}},
			})
		}
		results, err := repo.ExecuteEventItems(ctx, requests)
		if err != nil {
			return err
		}
		for i, result := range results {
			m := members[i]
			s.applyInventoryResult(m.live, result.Character)
			if !result.Replayed && len(result.Changes) > 0 {
				if visit := s.sceneFor(m.live, m.sink); visit != nil {
					_ = s.writeScenePackets(visit, protocol.ModifyInventory(result.Changes))
				}
			}
		}
		return nil
	}()
}
