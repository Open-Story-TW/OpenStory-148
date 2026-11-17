package world

import (
	"context"
	"fmt"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type hoblinPQState struct {
	mu      sync.Mutex
	rewards map[int64]int64
	rescues int
}

func (s *Server) rescueHoblinEscort(inst *eventInstance, key mapKey, room *gameplayRoom, escort *gameplayMob, reporterID int64) bool {
	inst.hoblin.mu.Lock()
	defer inst.hoblin.mu.Unlock()
	if !time.Now().Before(inst.deadline) || s.eventOf(reporterID) != inst {
		return false
	}
	leader := s.partySnapshotOf(reporterID).LeaderID
	for _, member := range s.activeEventMembers(inst, key.mapID) {
		if partySession(member.live).CharacterID != leader {
			continue
		}
		inst.hoblin.rescues++
		op := fmt.Sprintf("hoblin-rescue-%s-%d", inst.id, inst.hoblin.rescues)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := s.applyEventExchange(ctx, member, op, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(leader) != inst || session.Character.MapID != key.mapID {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{
				Items: []domain.ScriptItemDelta{{TemplateID: content.HoblinGlacialWater, Quantity: -1}},
			}, nil
		})
		cancel()
		if err != nil {
			return false
		}
		g := s.gameplay
		g.mu.Lock()
		if g.rooms[key] != room {
			g.mu.Unlock()
			return false
		}
		escort.hp = escort.stats.HP
		packets := roomBroadcast(room, protocol.DamageEscort(escort.objectID, 0, int(escort.hp), int(escort.stats.HP)), -1)
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("隊長用冰河水救回了邪摩斯。"), -1)...)
		g.mu.Unlock()
		deliver(packets)
		return true
	}
	return false
}

type hoblinArena struct {
	seal     *roomReactor
	king     content.Mob
	ending   content.Mob
	summonAt time.Time
}

func (g *gameplaySpace) prepareHoblinPQ(inst *eventInstance, hp int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, mapID := range []int{content.HoblinEscortFirst, content.HoblinEscortSecond, content.HoblinArena} {
		template := content.HoblinEscort
		if mapID == content.HoblinArena {
			template = content.HoblinArenaEscort
		}
		if err := prepareEscortRoom(g.rooms[mapKey{inst.channel, mapID}], template, hp); err != nil {
			return err
		}
	}
	king, err := g.catalog.Mob(content.HoblinKing)
	if err != nil || len(king.Revive) != 1 || king.Revive[0] != 9300283 || king.HP <= 0 {
		return domain.ErrInvalidInput
	}
	ending, err := g.catalog.Mob(king.Revive[0])
	if err != nil || ending.HP <= 0 || ending.RemoveAfter <= 0 {
		return domain.ErrInvalidInput
	}
	def, err := content.HoblinSeal(wzRoot())
	if err != nil {
		return err
	}
	oid := g.alloc()
	if oid == 0 {
		return domain.ErrInvalidInput
	}
	room := g.rooms[mapKey{inst.channel, content.HoblinArena}]
	seal := &roomReactor{objectID: oid, def: def}
	if room.reactors == nil {
		room.reactors = make(map[int32]*roomReactor)
	}
	room.reactors[oid] = seal
	room.rex = &hoblinArena{seal: seal, king: king, ending: ending}
	return nil
}

func (g *gameplaySpace) summonRex(room *gameplayRoom, now time.Time) []roomDelivery {
	arena := room.rex
	if arena.summonAt.IsZero() || now.Before(arena.summonAt) || room.bossForms != nil ||
		room.escort.mob.hp <= 0 {
		return nil
	}
	life := content.Life{Kind: "m", X: 332, Y: 173, Foothold: 7, RX0: -607, RX1: 594, RespawnSeconds: -1}
	king, packets := g.spawnEventBossForm(room, life, arena.king, room.escort.mob.controller, now)
	if king == nil {
		return nil
	}
	room.bossForms = &eventBossForms{active: king, next: []content.Mob{arena.ending}}
	arena.seal.state = 1
	def := arena.seal.def
	packets = append(packets, roomBroadcast(room, protocol.ReactorState(arena.seal.objectID, 1, def.X, def.Y), -1)...)
	packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("侏儒帝王雷克斯掙脫了封印！"), -1)...)
	return packets
}

func (s *Server) hoblinAmbush(ctx context.Context, actor domain.CharacterSession, mover eventOccupant) (bool, error) {
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.hoblin == nil || actor.Character.MapID != content.HoblinAmbush ||
		s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return false, nil
	}
	inst.hoblin.mu.Lock()
	defer inst.hoblin.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) ||
		len(s.activeEventMembers(inst, content.HoblinAmbush)) < inst.definition.Keep ||
		s.mapMonsterCount(inst.channel, content.HoblinAmbush) != 0 {
		return false, nil
	}
	return true, s.moveHoblinParty(ctx, inst, mover, content.HoblinAmbush, content.HoblinEscortFirst)
}

func (s *Server) moveHoblinParty(ctx context.Context, inst *eventInstance, mover eventOccupant, room, next int) error {
	m, err := s.gameplay.catalog.Map(next)
	if err != nil {
		return err
	}
	entry, ok := m.Spawn()
	if !ok {
		return domain.ErrInvalidInput
	}
	return s.moveEventRoom(ctx, inst, room, mover, next, entry)
}

func (s *Server) hoblinPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	roomID := actor.Character.MapID
	if roomID == content.HoblinResult && p.Script == "shammos_Revive" {
		m, err := s.gameplay.catalog.Map(content.HoblinTown)
		if err != nil {
			return true, err
		}
		entry, ok := m.Spawn()
		if !ok {
			return true, domain.ErrInvalidInput
		}
		s.cancelNPCScript(live)
		s.closeEconomyDialog(live)
		return true, s.warpGameplay(ctx, channel, live, sink, m.ID, entry)
	}
	isRoadEnd := p.Script == "shammos_Next" && (roomID == content.HoblinEscortFirst || roomID == content.HoblinEscortSecond)
	isBossEnd := p.Script == "shammos_Bossout" && roomID == content.HoblinArena
	if !isRoadEnd && !isBossEnd {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.hoblin == nil || inst.channel != channel ||
		s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return true, eventPortalNotice(sink, "請讓隊長與全隊一同前進。")
	}
	inst.hoblin.mu.Lock()
	defer inst.hoblin.mu.Unlock()
	group := s.activeEventMembers(inst, roomID)
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) || len(group) < inst.definition.Keep {
		return true, eventPortalNotice(sink, "請等全隊抵達再一起前進。")
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, roomID}]
	ready := room != nil && room.escort != nil && room.escort.done
	if isBossEnd {
		ready = room != nil && room.bossForms != nil && room.bossForms.done
	} else if ready {
		next := g.rooms[mapKey{channel, roomID + 100}]
		ready = next != nil && next.escort != nil
		if ready {
			next.escort.mob.hp = room.escort.mob.hp
		}
	}
	g.mu.Unlock()
	if !ready {
		return true, eventPortalNotice(sink, "必須完成這一關，才能開啟傳送點。")
	}
	if isBossEnd {
		return true, s.finishHoblinPQ(ctx, inst, group)
	}
	return true, s.moveHoblinParty(ctx, inst, eventOccupant{live, sink}, roomID, roomID+100)
}

func (s *Server) finishHoblinPQ(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	for _, member := range group {
		id := partySession(member.live).CharacterID
		err := s.applyEventExchange(ctx, member, "hoblin-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(id) != inst || session.Character.MapID != content.HoblinArena {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			if _, ok := inst.hoblin.rewards[id]; !ok {
				level := max(1, session.Character.Level)
				scaled := level
				if level > 200 {
					scaled = 200 + level/10
				}
				denom := int64(2 * min(200, level))
				inst.hoblin.rewards[id] = (content.ExperienceForLevel(scaled)*15 + denom/2) / denom
			}
			return domain.ScriptExchange{
				EXP:   inst.hoblin.rewards[id],
				Items: []domain.ScriptItemDelta{{TemplateID: content.HoblinReward, Quantity: 1}},
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "hoblin-clear"}, Delta: 1}},
			}, nil
		})
		if err != nil {
			return err
		}
	}
	s.broadcastInventoryPacket(inst.channel, content.HoblinArena, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.eventEndAt(inst, content.HoblinResult)
	return nil
}
