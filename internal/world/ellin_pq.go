package world

import (
	"context"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

type ellinPQState struct {
	mu            sync.Mutex
	marblesTurned bool
	reactorUses   uint64
	forms         []content.Mob
	rewards       map[int64]ellinReward
}

type ellinReward struct {
	exp   int64
	items []domain.ScriptItemDelta
}

func ellinCourseItems(c domain.Character) []domain.ScriptItemDelta {
	var items []domain.ScriptItemDelta
	for _, id := range [...]int{content.EllinStoneShard, content.EllinDilutedPoison, content.EllinPurpleStone,
		content.EllinMonsterMarble, content.EllinPurifyMarble} {
		if qty := henesysItemCount(c, id); qty > 0 {
			items = append(items, domain.ScriptItemDelta{TemplateID: id, Quantity: -qty})
		}
	}
	return items
}

func (s *Server) prepareEllinPQ(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	repo, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return domain.ErrForbidden
	}
	for _, mapID := range inst.definition.Maps {
		if err := repo.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, mapID); err != nil {
			return err
		}
	}
	for _, member := range group {
		if err := s.applyEventExchange(ctx, member, "ellin-enter-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			return domain.ScriptExchange{Items: ellinCourseItems(session.Character)}, nil
		}); err != nil {
			return err
		}
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	for id := content.EllinPoisonGolem; id <= content.EllinClearForm; id++ {
		mob, err := g.catalog.Mob(id)
		if err != nil || mob.HP <= 0 || (id < content.EllinClearForm && (len(mob.Revive) != 1 || mob.Revive[0] != id+1)) ||
			(id == content.EllinClearForm && !mob.Invincible) {
			return domain.ErrInvalidInput
		}
		inst.ellin.forms = append(inst.ellin.forms, mob)
	}
	for _, mapID := range [...]int{content.EllinBoulder, content.EllinBaskets, content.EllinAltar} {
		room := g.rooms[mapKey{inst.channel, mapID}]
		if room == nil {
			return domain.ErrNotFound
		}
		defs, err := content.EllinReactors(wzRoot(), mapID)
		if err != nil {
			return err
		}
		if mapID == content.EllinBaskets && len(defs) != 9 {
			return domain.ErrInvalidInput
		}
		if room.reactors == nil {
			room.reactors = make(map[int32]*roomReactor)
		}
		correct := -1
		if mapID == content.EllinBaskets {
			correct = rand.IntN(len(defs))
		}
		for i, def := range defs {
			oid := g.alloc()
			if oid == 0 {
				return domain.ErrForbidden
			}
			reactor := &roomReactor{objectID: oid, def: def}
			if i == correct {
				reactor.state = 2
			}
			room.reactors[oid] = reactor
		}
	}
	return nil
}

func (s *Server) ellinPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	room := actor.Character.MapID
	if room == content.EllinMaze && p.Script != "" {
		return s.ellinMazeDoor(ctx, channel, live, sink, p)
	}
	if !((p.Script == "party6_stage" && room >= content.EllinEntrance && room <= content.EllinBoulder) ||
		(p.Script == "party6_out" && room == content.EllinAltar)) {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ellin == nil || inst.channel != channel || s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return true, eventPortalNotice(sink, "請讓隊長與隊員一起前進。")
	}
	inst.ellin.mu.Lock()
	defer inst.ellin.mu.Unlock()
	group := s.activeEventMembers(inst, room)
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) || len(group) < inst.definition.Keep {
		return true, eventPortalNotice(sink, "請等隊員抵達再一起前進。")
	}
	ready := true
	switch room {
	case content.EllinGlade:
		ready = s.mapMonsterCount(channel, room) == 0
	case content.EllinBoulder:
		ready = s.ellinSpineCleared(channel)
	case content.EllinAltar:
		return true, s.finishEllinPQ(ctx, inst, group, sink)
	}
	if !ready {
		return true, eventPortalNotice(sink, "完成這一關後，傳送點才會開啟。")
	}
	return true, s.moveEllinRoom(ctx, inst, eventOccupant{live, sink}, room, room+100, false)
}

func (s *Server) ellinMazeDoor(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	if len(p.Name) != 4 || len(p.Script) < len("party6_stage5") || p.Script[:len("party6_stage5")] != "party6_stage5" {
		return false, nil
	}
	inst := s.eventOf(partySession(live).CharacterID)
	if inst == nil || inst.ellin == nil || inst.channel != channel {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	room, err := strconv.Atoi(p.Name[:3])
	if err != nil || room < 1 || room > 11 {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	switch p.Name[3] {
	case 'N':
		room -= 4
	case 'S':
		room += 4
	case 'W':
		room--
	case 'E':
		room++
	}
	m, err := s.gameplay.catalog.Map(content.EllinMaze)
	if err != nil {
		return true, err
	}
	target := strconv.Itoa(room) + "st"
	if room > 0 && room < 10 {
		target = "0" + target
	}
	entry, ok := m.Portal(target)
	if !ok {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	return true, s.warpEventMember(ctx, inst, content.EllinMaze, eventOccupant{live, sink}, content.EllinMaze, entry)
}

func (s *Server) moveEllinRoom(ctx context.Context, inst *eventInstance, actor eventOccupant, from, to int, inDialogue bool) error {
	m, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return err
	}
	entry, ok := m.Spawn()
	if !ok {
		return domain.ErrInvalidInput
	}
	if inDialogue {
		s.warpEventPeers(ctx, inst, from, actor.live, to, entry)
		return s.warpEventMember(ctx, inst, from, actor, to, entry)
	}
	return s.moveEventRoom(ctx, inst, from, actor, to, entry)
}

func (s *Server) ellinNPC(ctx context.Context, actor domain.CharacterSession, member eventOccupant, npc int) (string, error) {
	room := actor.Character.MapID
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ellin == nil {
		return "unavailable", nil
	}
	pq := inst.ellin
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) {
		return "unavailable", nil
	}
	if room != content.EllinMaze && room != content.EllinCapture && room != content.EllinBaskets {
		return "objective", nil
	}
	lead := s.partySnapshotOf(actor.CharacterID).LeaderID == actor.CharacterID
	fullGroup := len(s.activeEventMembers(inst, room)) >= inst.definition.Keep
	switch room {
	case content.EllinMaze:
		if npc != 2133001 || !lead || !fullGroup || !within(actor.Character.X, actor.Character.Y, 1274, 1060, 160) {
			return "maze", nil
		}
	case content.EllinCapture:
		if npc != 2133001 {
			return "unavailable", nil
		}
		if !pq.marblesTurned && henesysItemCount(actor.Character, content.EllinMonsterMarble) >= 20 && lead && fullGroup {
			err := s.applyEventExchange(ctx, member, "ellin-marbles-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
				if s.eventOf(session.CharacterID) != inst || session.Character.MapID != room {
					return domain.ScriptExchange{}, domain.ErrStaleOwner
				}
				return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: content.EllinMonsterMarble, Quantity: -20}}}, nil
			})
			if err != nil {
				return "", err
			}
			pq.marblesTurned = true
		}
		if !pq.marblesTurned || !lead || !fullGroup {
			if henesysItemCount(actor.Character, content.EllinPurifyMarble) == 0 {
				err := s.applyEventExchange(ctx, member, "ellin-supply-"+platform.NewID(), func(session domain.CharacterSession) (domain.ScriptExchange, error) {
					if s.eventOf(session.CharacterID) != inst || session.Character.MapID != room {
						return domain.ScriptExchange{}, domain.ErrStaleOwner
					}
					if henesysItemCount(session.Character, content.EllinPurifyMarble) != 0 {
						return domain.ScriptExchange{}, nil
					}
					return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: content.EllinPurifyMarble, Quantity: 10}}}, nil
				})
				if err != nil {
					return "", err
				}
			}
			return "capture", nil
		}
	case content.EllinBaskets:
		if npc != 2133004 || !fullGroup || henesysItemCount(actor.Character, content.EllinPurpleStone) == 0 {
			return "stone", nil
		}
	}
	return "moved", s.moveEllinRoom(ctx, inst, member, room, room+100, true)
}

func (s *Server) finishEllinPQ(ctx context.Context, inst *eventInstance, group []eventOccupant, sink packetSink) error {
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, content.EllinAltar}]
	cleared := room != nil && room.bossForms != nil && room.bossForms.active.life.TemplateID == content.EllinClearForm
	g.mu.Unlock()
	if !cleared {
		return eventPortalNotice(sink, "擊倒毒石巨人後才能領取獎勵。")
	}
	for _, member := range group {
		peer := partySession(member.live)
		err := s.applyEventExchange(ctx, member, "ellin-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(session.CharacterID) != inst || session.Character.MapID != content.EllinAltar {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			reward, planned := inst.ellin.rewards[peer.CharacterID]
			if !planned {
				level := max(1, session.Character.Level)
				scaled := level
				if level > 120 {
					scaled = 120 + level/10
				}
				denominator := int64(min(120, level))
				reward.exp = (content.ExperienceForLevel(scaled) + denominator/2) / denominator
				reward.items = append(ellinCourseItems(session.Character), domain.ScriptItemDelta{TemplateID: content.EllinAltairFragment, Quantity: 1})
				inst.ellin.rewards[peer.CharacterID] = reward
			}
			return domain.ScriptExchange{
				EXP:   reward.exp,
				Items: reward.items,
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "ellin-clear"}, Delta: 1}},
			}, nil
		})
		if err != nil {
			return err
		}
	}
	s.broadcastInventoryPacket(inst.channel, content.EllinAltar, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.eventEndAt(inst, content.EllinResult)
	return nil
}
