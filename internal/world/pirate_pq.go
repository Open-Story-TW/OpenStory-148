package world

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type piratePQState struct {
	mu       sync.Mutex
	section  int
	deadline atomic.Int64
	medals   int
	chests   int
	boss     bool
	rewards  map[int64]piratePQReward
}

type piratePQReward struct {
	exp   int64
	items []domain.ScriptItemDelta
}

var pirateMedals = [...]int{4001120, 4001121, 4001122}

func (p *piratePQState) startSection(mapID int, now time.Time) {
	section, seconds := content.PiratePQSection(mapID)
	if section > p.section {
		p.section = section
		p.deadline.Store(now.Add(time.Duration(seconds) * time.Second).UnixNano())
	}
}

func (s *Server) preparePiratePQ(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	drops, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return domain.ErrForbidden
	}
	for _, id := range inst.definition.Maps {
		if err := drops.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, id); err != nil {
			return err
		}
	}
	for _, member := range group {
		if err := s.applyEventExchange(ctx, member, "pirate-start-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			return domain.ScriptExchange{Items: pirateCourseItems(session.Character)}, nil
		}); err != nil {
			return err
		}
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, mapID := range inst.definition.Maps {
		room := g.rooms[mapKey{inst.channel, mapID}]
		if err := g.addPirateReactors(room); err != nil {
			return err
		}
		switch mapID {
		case content.PiratePQLower, content.PiratePQUpper:
			for _, guard := range [...]struct{ id, x, y int }{{9300124, 430, 75}, {9300125, 1600, 75}, {9300124, 430, 238}, {9300125, 1600, 238}} {
				for i := 0; i < 5; i++ {
					if _, err := g.pirateSpawn(room, guard.id, guard.x+i*9, guard.y-12); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func pirateCourseItems(c domain.Character) []domain.ScriptItemDelta {
	var items []domain.ScriptItemDelta
	for _, id := range []int{content.PiratePQKey, pirateMedals[0], pirateMedals[1], pirateMedals[2]} {
		if n := henesysItemCount(c, id); n > 0 {
			items = append(items, domain.ScriptItemDelta{TemplateID: id, Quantity: -n})
		}
	}
	return items
}

func (g *gameplaySpace) pirateSpawn(room *gameplayRoom, id, x, y int) (*gameplayMob, error) {
	stats, err := g.catalog.Mob(id)
	if err != nil {
		return nil, err
	}
	ground, ok := groundBelow(room.definition, x, y)
	if !ok || ground-y > 100 {
		return nil, fmt.Errorf("pirate quest map %d has no foothold under (%d,%d)", room.definition.ID, x, y)
	}
	life := content.Life{Kind: "m", TemplateID: id, X: x, Y: ground - 1, RX0: x - 95, RX1: x + 95, RespawnSeconds: -1}
	for _, fh := range room.definition.Footholds {
		if min(fh.X1, fh.X2) <= x && x <= max(fh.X1, fh.X2) && fh.X1 != fh.X2 &&
			fh.Y1+(fh.Y2-fh.Y1)*(x-fh.X1)/(fh.X2-fh.X1) == ground {
			life.Foothold = fh.ID
			break
		}
	}
	oid := g.alloc()
	if oid == 0 || life.Foothold == 0 {
		return nil, domain.ErrForbidden
	}
	mob := &gameplayMob{objectID: oid, origin: life, life: life, stats: stats, hp: stats.HP}
	room.mobs[oid] = mob
	return mob, nil
}

func (s *Server) piratePQPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	actor := partySession(live)
	room := actor.Character.MapID
	var mapID int
	switch portal.Script {
	case "davy_next0", "davy_next1", "davy_next2", "davy_next3", "davy_next4":
		if room < content.PiratePQShore || room > content.PiratePQDoors || room%100 != 0 {
			return false, nil
		}
		mapID = room + 100
	case "davy2_hd1", "davy3_hd1":
		if room != content.PiratePQLower && room != content.PiratePQUpper {
			return false, nil
		}
		mapID = room + 2
	default:
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.pirate == nil || inst.channel != channel {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	state := inst.pirate
	state.mu.Lock()
	defer state.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.currentDeadline()) || partySession(live).Character.MapID != room {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	destination, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return true, err
	}
	entry, ok := destination.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	mover := eventOccupant{live, sink}
	if mapID == room+2 {
		return true, s.warpEventMember(ctx, inst, room, mover, mapID, entry)
	}
	if s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID || len(s.activeEventMembers(inst, room)) < inst.definition.Keep {
		return true, eventPortalNotice(sink, "請集合隊員，讓隊長前往下一關。")
	}
	var ready bool
	switch room {
	case content.PiratePQMedals:
		ready = state.medals == len(pirateMedals)
	case content.PiratePQDoors:
		ready = s.pirateDoorsClosed(inst.channel)
	default:
		ready = s.mapMonsterCount(inst.channel, room) == 0
	}
	if !ready {
		return true, eventPortalNotice(sink, "先完成這一關的任務，才能開啟傳送點。")
	}
	if mapID == content.PiratePQCaptain && !state.boss {
		sets := [3][2]int{{9300107, 9300119}, {9300119, 9300105}, {9300106, 9300105}}
		id := sets[min(state.chests, 2)][rand.IntN(2)]
		g := s.gameplay
		g.mu.Lock()
		_, err = g.pirateSpawn(g.rooms[mapKey{channel, mapID}], id, 411, 224)
		g.mu.Unlock()
		if err != nil {
			return true, err
		}
		state.boss = true
	}
	state.startSection(mapID, time.Now())
	return true, s.moveEventRoom(ctx, inst, room, mover, mapID, entry)
}

func (s *Server) piratePQTalk(ctx context.Context, actor domain.CharacterSession, member eventOccupant) (map[string]any, error) {
	inst := s.eventOf(actor.CharacterID)
	result := map[string]any{"state": "unavailable"}
	if inst == nil || inst.pirate == nil {
		return result, nil
	}
	state := inst.pirate
	state.mu.Lock()
	defer state.mu.Unlock()
	roomID, id := actor.Character.MapID, actor.CharacterID
	if s.eventOf(id) != inst || !time.Now().Before(inst.currentDeadline()) {
		return result, nil
	}
	if roomID == content.PiratePQMedals || roomID == content.PiratePQCaptain {
		if s.partySnapshotOf(id).LeaderID != id {
			result["state"] = "not_leader"
			return result, nil
		}
	}
	switch roomID {
	case content.PiratePQMedals:
		if state.medals < len(pirateMedals) {
			item := pirateMedals[state.medals]
			err := s.applyEventExchange(ctx, member, fmt.Sprintf("pirate-medal-%s-%d", inst.id, state.medals), func(session domain.CharacterSession) (domain.ScriptExchange, error) {
				if session.Character.MapID != roomID || s.eventOf(id) != inst {
					return domain.ScriptExchange{}, domain.ErrStaleOwner
				}
				return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: item, Quantity: -20}}}, nil
			})
			if err == nil {
				state.medals++
			} else if !errors.Is(err, domain.ErrInsufficient) {
				return result, err
			}
		}
		result["state"] = "medals_done"
		if state.medals < len(pirateMedals) {
			result["state"], result["item"] = "medals", int64(pirateMedals[state.medals])
		}
	case content.PiratePQCaptain:
		result["state"] = "captain"
		if state.boss && s.mapMonsterCount(inst.channel, roomID) == 0 {
			if done, err := s.finishPiratePQ(ctx, inst); done || err != nil {
				result["state"] = "finished"
				return result, err
			}
		}
	case content.PiratePQDoors:
		result["state"] = "doors"
	default:
		result["state"] = "guards"
	}
	return result, nil
}

func (s *Server) finishPiratePQ(ctx context.Context, inst *eventInstance) (bool, error) {
	group := s.activeEventMembers(inst, content.PiratePQCaptain)
	if len(group) < inst.definition.Keep {
		return false, nil
	}
	for _, member := range group {
		peer := partySession(member.live)
		id, level := peer.CharacterID, max(1, peer.Character.Level)
		reward, ok := inst.pirate.rewards[id]
		if !ok {
			scaled := level
			if level > 120 {
				scaled = 120 + level/10
			}
			denom := int64(2 * min(120, level))
			reward = piratePQReward{
				exp:   (content.ExperienceForLevel(scaled)*10 + denom/2) / denom,
				items: append(pirateCourseItems(peer.Character), domain.ScriptItemDelta{TemplateID: content.PiratePQBadge, Quantity: 1}),
			}
			inst.pirate.rewards[id] = reward
		}
		if err := s.applyEventExchange(ctx, member, "pirate-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if session.Character.MapID != content.PiratePQCaptain || s.eventOf(id) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{Items: reward.items, EXP: reward.exp,
				Logs: []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "pirate-clear"}, Delta: 1}}}, nil
		}); err != nil {
			return false, err
		}
	}
	s.broadcastInventoryPacket(inst.channel, content.PiratePQCaptain, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.eventEndAt(inst, content.PiratePQResult)
	return true, nil
}
