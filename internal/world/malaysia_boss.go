package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type malaysiaOfferingRepository interface {
	ConsumeMalaysiaBossOffering(context.Context, domain.CharacterSession, int32, int) error
}

type malaysiaStatue struct {
	definition content.MalaysiaBossStatue
	reactor    *roomReactor
	offering   bool
	form       int
}

func (g *gameplaySpace) addMalaysiaStatues(room *gameplayRoom, mapID int) error {
	if mapID != content.MalaysiaBossArena {
		return nil
	}
	if room.reactors == nil {
		room.reactors = make(map[int32]*roomReactor, 2)
	}
	room.malaysiaStatues = make(map[int]*malaysiaStatue, 2)
	for _, def := range content.MalaysiaBossStatues() {
		oid := g.alloc()
		if oid == 0 {
			return errors.New("unable to allocate a theme park statue")
		}
		reactor := &roomReactor{objectID: oid,
			def: content.Reactor{TemplateID: def.Reactor, X: def.X, Y: def.Y, Respawn: -1}}
		room.reactors[oid] = reactor
		room.malaysiaStatues[def.Reactor] = &malaysiaStatue{definition: def, reactor: reactor}
	}
	return nil
}

func (g *gameplaySpace) newMalaysiaBoss(def content.MalaysiaBossStatue, stats content.Mob) *gameplayMob {
	oid := g.alloc()
	if oid == 0 {
		return nil
	}
	life := content.Life{Kind: "m", TemplateID: stats.ID, X: def.X, Y: 640, Foothold: def.Foothold, RespawnSeconds: -1}
	return &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
}

func spawnMalaysiaBoss(room *gameplayRoom, mob *gameplayMob) []roomDelivery {
	room.mobs[mob.objectID] = mob
	now := time.Now()
	updates := roomBroadcast(room, mob.spawnPacket(false, now), -1)
	for actorID, member := range room.members {
		mob.controller = actorID
		updates = append(updates, roomDelivery{member.sink, mob.spawnPacket(true, now)})
		break
	}
	return updates
}

func (s *Server) offerMalaysiaBossItem(ctx context.Context, session domain.CharacterSession, drop domain.ItemDrop) {
	if drop.MapID != content.MalaysiaBossArena || session.Character.MapID != drop.MapID || drop.ChannelID != session.ChannelID ||
		drop.TemplateID != content.MalaysiaBossOffering || drop.Quantity != 1 || drop.OwnerID != session.CharacterID ||
		s.events == nil || s.gameplay == nil {
		return
	}
	def, present := content.MalaysiaOfferingStatue(drop.X, drop.Y)
	repo, supported := s.repo.(malaysiaOfferingRepository)
	attempt := s.eventOf(session.CharacterID)
	if !present || !supported || attempt == nil {
		return
	}
	g := s.gameplay
	stats, err := g.catalog.Mob(def.FirstForm)
	if err != nil {
		return
	}
	bodyStats, err := g.catalog.Mob(def.FirstForm + 1)
	if err != nil {
		return
	}
	key := mapKey{channel: drop.ChannelID, mapID: drop.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || !s.expeditionCombatAllowedLocked(key, session.CharacterID) {
		g.mu.Unlock()
		return
	}
	statue := room.malaysiaStatues[def.Reactor]
	if statue == nil || statue.offering || statue.form != 0 {
		g.mu.Unlock()
		return
	}
	opener := g.newMalaysiaBoss(def, stats)
	body := g.newMalaysiaBoss(def, bodyStats)
	if opener == nil || body == nil {
		g.mu.Unlock()
		return
	}
	// Both WZ opening forms have only a one-pixel stand sprite. Their
	// removeAfter=2 revive starts the attackable boss after the animation.
	opener.fake = true
	statue.offering = true
	g.mu.Unlock()

	storeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err = repo.ConsumeMalaysiaBossOffering(storeCtx, session, drop.ObjectID, def.Reactor)
	cancel()

	g.mu.Lock()
	statue.offering = false
	s.events.mu.Lock()
	active := !attempt.ended && len(attempt.members) >= attempt.definition.Keep
	s.events.mu.Unlock()
	if err != nil || g.rooms[key] != room || !active {
		g.mu.Unlock()
		if err == nil {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		return
	}
	statue.form = def.FirstForm
	statue.reactor.state = 1
	updates := roomBroadcast(room, protocol.ReactorState(statue.reactor.objectID, 1, def.X, def.Y), -1)
	updates = append(updates, spawnMalaysiaBoss(room, opener)...)
	g.mu.Unlock()
	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	deliver(updates)
	time.AfterFunc(2*time.Second, func() {
		s.reviveMalaysiaBossOpener(key, room, statue, attempt, opener, body)
	})
}

func (s *Server) reviveMalaysiaBossOpener(key mapKey, room *gameplayRoom, statue *malaysiaStatue, attempt *eventInstance, opener, body *gameplayMob) {
	g := s.gameplay
	g.mu.Lock()
	s.events.mu.Lock()
	active := !attempt.ended && len(attempt.members) >= attempt.definition.Keep
	s.events.mu.Unlock()
	if !active || g.rooms[key] != room || room.mobs[opener.objectID] != opener || statue.form != opener.stats.ID {
		g.mu.Unlock()
		return
	}
	delete(room.mobs, opener.objectID)
	statue.form = body.stats.ID
	updates := roomBroadcast(room, protocol.RemoveMonster(opener.objectID), -1)
	updates = append(updates, spawnMalaysiaBoss(room, body)...)
	g.mu.Unlock()
	deliver(updates)
}

func (s *Server) malaysiaBossFormDefeated(key mapKey, actor int64, form int) {
	if s.events == nil || s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	var updates []roomDelivery
	for _, statue := range room.malaysiaStatues {
		if statue.form != form || form <= statue.definition.FirstForm {
			continue
		}
		if form == statue.definition.LastForm {
			statue.form = -1
			updates = roomBroadcast(room, protocol.PortalTrainingMessage("已擊敗首領。拾取戰利品後，可以找艾爾多魯離開。"), -1)
			break
		}
		stats, err := g.catalog.Mob(form + 1)
		if err == nil {
			if mob := g.newMalaysiaBoss(statue.definition, stats); mob != nil {
				statue.form = stats.ID
				updates = spawnMalaysiaBoss(room, mob)
			}
		}
		break
	}
	g.mu.Unlock()
	deliver(updates)
}
