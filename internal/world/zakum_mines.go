package world

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func zakumMineDrop(templateID int) int {
	switch templateID {
	case 2112000, 2112008:
		return 2000004
	case 2112001, 2112009:
		return 2020001
	case 2112002:
		return 2022002
	case 2112003, 2112010:
		return 2000005
	case 2112004, 2112011:
		return content.ZakumMineKey
	case 2112005, 2112012:
		return content.ZakumMineDocument
	case 2112007:
		return 2022001
	default:
		return 0
	}
}

func (s *Server) hitZakumMineReactor(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	actor := partySession(live)
	key := mapKey{channel, actor.Character.MapID}
	if !content.ZakumMineMap(key.mapID) {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	visit := s.sceneFor(live, sink)
	if inst == nil || inst.definition.Name != content.ZakumMineName || visit == nil || actor.Character.HP <= 0 || s.tradeActive(live) {
		return true, nil
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[actor.CharacterID] == nil || room.members[actor.CharacterID].visit != visit {
		g.mu.Unlock()
		return true, nil
	}
	reactor := room.reactors[oid]
	if reactor == nil || reactor.mineHits == 0 || reactor.gone || reactor.finishing ||
		abs(reactor.def.X-actor.Character.X) > 145 || abs(reactor.def.Y-actor.Character.Y) > 90 || time.Since(reactor.lastHit) < 170*time.Millisecond {
		g.mu.Unlock()
		return true, nil
	}
	reactor.lastHit = time.Now()
	reactor.state++
	state, def := reactor.state, reactor.def
	if state < reactor.mineHits {
		g.mu.Unlock()
		s.broadcastInventoryPacket(channel, key.mapID, protocol.ReactorState(oid, state, def.X, def.Y))
		return true, nil
	}
	reactor.finishing = true
	g.mu.Unlock()

	var err error
	if item := zakumMineDrop(def.TemplateID); item != 0 {
		x, y := s.gatherDropPoint(key.mapID, def.X, def.Y, 5)
		err = s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("zakum-mine-hit-%s-%d", inst.id, oid), func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if !s.sceneCurrent(visit) || s.eventOf(session.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{Drops: []domain.ScriptDrop{{TemplateID: item, Quantity: 1, X: x, Y: y}}}, nil
		})
	}
	if def.TemplateID == 2111000 {
		err = s.spawnReactorMonster(key, def, 9300004, 3)
	}
	g.mu.Lock()
	reactor.finishing = false
	if err != nil {
		reactor.state--
		g.mu.Unlock()
		return true, sink.WritePacket(protocol.PortalTrainingMessage("暫時無法打開，請再試一次。"))
	}
	current := g.rooms[key] == room
	reactor.gone = true
	g.mu.Unlock()
	if current {
		s.broadcastInventoryPacket(channel, key.mapID, protocol.ReactorState(oid, state, def.X, def.Y))
		s.broadcastInventoryPacket(channel, key.mapID, protocol.ReactorLeave(oid, state, def.X, def.Y))
	}
	if def.TemplateID == 2110000 && current {
		field, err := g.catalog.Map(content.ZakumMineMain)
		if err != nil {
			return true, err
		}
		entry, ok := field.Spawn()
		if !ok {
			return true, domain.ErrNotFound
		}
		if err := sink.WritePacket(protocol.PortalTrainingMessage("一股未知的力量將你送回調查起點。")); err != nil {
			return true, err
		}
		return true, s.warpEventMember(ctx, inst, key.mapID, eventOccupant{live, sink}, field.ID, entry)
	}
	return true, nil
}

type zakumMineRewardRepository interface {
	CompleteZakumMine(context.Context, []domain.ScriptExchange) ([]domain.ScriptExchangeResult, error)
}

type zakumMineOfferingRepository interface {
	OfferZakumMineKeys(context.Context, domain.CharacterSession, domain.ItemDrop, int, int) (domain.ScriptExchangeResult, error)
}

func (s *Server) offerZakumMineKeys(ctx context.Context, live *liveCharacter, session domain.CharacterSession, drop domain.ItemDrop) {
	if drop.MapID != 280011005 || drop.TemplateID != content.ZakumMineKey || drop.Quantity != 7 || !drop.PlayerDrop {
		return
	}
	repo, ok := s.repo.(zakumMineOfferingRepository)
	inst := s.eventOf(session.CharacterID)
	if !ok || inst == nil || inst.definition.Name != content.ZakumMineName {
		return
	}
	g := s.gameplay
	key := mapKey{drop.ChannelID, drop.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	var chest *roomReactor
	if room != nil {
		for _, r := range room.reactors {
			dx, dy := drop.X-r.def.X, drop.Y-r.def.Y
			if r.def.TemplateID == content.ZakumOreChest && !r.finishing && r.state == 0 && dx >= -78 && dx <= 29 && dy >= -67 && dy <= 25 {
				chest = r
				chest.finishing = true
				break
			}
		}
	}
	g.mu.Unlock()
	if chest == nil {
		return
	}
	x, y := s.gatherDropPoint(key.mapID, chest.def.X, chest.def.Y, 8)
	result, err := repo.OfferZakumMineKeys(ctx, s.skillSession(live), drop, x, y)
	g.mu.Lock()
	chest.finishing = false
	if err != nil || g.rooms[key] != room {
		g.mu.Unlock()
		return
	}
	chest.state = 1
	g.mu.Unlock()
	s.applyInventoryResult(live, result.Character)
	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	s.broadcastInventoryPacket(drop.ChannelID, drop.MapID, protocol.ReactorState(chest.objectID, 1, chest.def.X, chest.def.Y))
	for _, ore := range result.Drops {
		s.broadcastCombatDrop(ore)
	}
}

func (a *worldScript) finishZakumMine(ctx context.Context, actor domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	value := func(reason string) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: reason}, nil
	}
	s := a.server
	inst := s.eventOf(actor.CharacterID)
	party := s.partySnapshotOf(actor.CharacterID)
	if len(command.Ints) != 0 || a.npcID != 2032002 || actor.Character.MapID != content.ZakumMineMain ||
		inst == nil || inst.definition.Name != content.ZakumMineName || party.LeaderID != 0 && party.LeaderID != actor.CharacterID {
		return value("leader")
	}
	repo, ok := s.repo.(zakumMineRewardRepository)
	if !ok {
		return value("unavailable")
	}
	members := s.eventOccupants(inst.channel, inst.definition.Maps)
	filtered := members[:0]
	for _, m := range members {
		if s.eventOf(partySession(m.live).CharacterID) == inst {
			filtered = append(filtered, m)
		}
	}
	members = filtered
	sort.Slice(members, func(i, j int) bool {
		return partySession(members[i].live).CharacterID < partySession(members[j].live).CharacterID
	})
	apply := func() (string, error) {
		for _, m := range members {
			m.live.persistMu.Lock()
			defer m.live.persistMu.Unlock()
		}
		leader := s.skillSession(a.live)
		if !s.sceneCurrent(s.sceneFor(a.live, a.sink)) || s.eventOf(leader.CharacterID) != inst || leader.Character.MapID != content.ZakumMineMain {
			return "unavailable", nil
		}
		if henesysItemCount(leader.Character, content.ZakumRoughOre) == 0 {
			return "ore", nil
		}
		bonus := henesysItemCount(leader.Character, content.ZakumMineDocument) >= 30
		requests := make([]domain.ScriptExchange, 0, len(members))
		for _, m := range members {
			member := s.skillSession(m.live)
			if member.Character.HP <= 0 || s.eventOf(member.CharacterID) != inst || !inst.definition.Stage(member.Character.MapID) {
				return "unavailable", nil
			}
			request := domain.ScriptExchange{Session: member, OperationID: "zakum-mine-reward-" + inst.id, EXP: 12000,
				Items: []domain.ScriptItemDelta{{TemplateID: content.ZakumFireOre, Quantity: 1}}}
			if bonus {
				request.EXP = 20000
				request.Items = append(request.Items, domain.ScriptItemDelta{TemplateID: 2030007, Quantity: 5})
			}
			if member.CharacterID == leader.CharacterID {
				request.Items = append(request.Items, domain.ScriptItemDelta{TemplateID: content.ZakumRoughOre, Quantity: -1})
				if bonus {
					request.Items = append(request.Items, domain.ScriptItemDelta{TemplateID: content.ZakumMineDocument, Quantity: -30})
				}
			}
			requests = append(requests, request)
		}
		results, err := repo.CompleteZakumMine(ctx, requests)
		if errors.Is(err, domain.ErrInsufficient) {
			return "bags", nil
		}
		if err != nil {
			return "unavailable", err
		}
		for i, res := range results {
			member := members[i]
			s.applyProgressResult(member.live, res.Character)
			if !res.Replayed {
				_ = member.sink.WritePacket(protocol.ModifyInventory(res.Changes))
				_ = member.sink.WritePacket(protocol.UpdateCombatStats(res.Character))
				_ = member.sink.WritePacket(protocol.ShowExperienceGain(requests[i].EXP, true))
			}
		}
		return "ok", nil
	}
	reason, err := apply()
	if err != nil {
		return scripting.CommandResult{}, err
	}
	if reason == "ok" {
		s.eventEnd(inst)
	}
	return value(reason)
}

func (s *Server) prepareZakumMine(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	writer, ok := s.repo.(interface {
		ExpireZakumMineDrops(context.Context, int, int, []int) error
	})
	if !ok {
		return domain.ErrForbidden
	}
	if err := writer.ExpireZakumMineDrops(ctx, s.cfg.WorldID, inst.channel, inst.definition.Maps); err != nil {
		return err
	}
	for _, member := range group {
		err := s.applyEventExchange(ctx, member, "zakum-mine-entry-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if session.Character.MapID != content.ZakumMineLobby || session.ChannelID != inst.channel || session.Character.HP <= 0 || s.eventOf(session.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			var removal []domain.ScriptItemDelta
			for _, item := range []int{content.ZakumMineKey, content.ZakumMineDocument, content.ZakumRoughOre} {
				if count := henesysItemCount(session.Character, item); count > 0 {
					removal = append(removal, domain.ScriptItemDelta{TemplateID: item, Quantity: -count})
				}
			}
			return domain.ScriptExchange{Items: removal}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
