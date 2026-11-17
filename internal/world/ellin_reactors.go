package world

import (
	"context"
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) ellinSpineCleared(channel int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	if room := g.rooms[mapKey{channel, content.EllinBoulder}]; room != nil {
		for _, reactor := range room.reactors {
			if reactor.def.TemplateID == 3009000 && reactor.state == 4 {
				return true
			}
		}
	}
	return false
}

// TWMS v148 uses click events (type 9) on all four Ellin reactor templates.
// The actor needs the reagent in their inventory before interacting.
func (s *Server) ellinReactorClick(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) error {
	actor := partySession(live)
	mapID := actor.Character.MapID
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ellin == nil || inst.channel != channel || actor.Character.HP <= 0 || s.tradeActive(live) {
		return nil
	}
	pq := inst.ellin
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) ||
		len(s.activeEventMembers(inst, mapID)) < inst.definition.Keep {
		return nil
	}
	g := s.gameplay
	key := mapKey{channel, mapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[actor.CharacterID] == nil || !sameDropSink(room.members[actor.CharacterID].sink, sink) {
		g.mu.Unlock()
		return nil
	}
	reactor := room.reactors[oid]
	if reactor == nil {
		g.mu.Unlock()
		return nil
	}
	reactorSnapshot := *reactor
	required, output, ready := ellinClickEffect(reactorSnapshot, actor.Character)
	def := reactor.def
	var ground int
	if output != 0 {
		x, y := ellinOutputPoint(def)
		var ok bool
		ground, ok = groundBelow(room.definition, x, y)
		ready = ready && ok
	}
	g.mu.Unlock()
	if !ready {
		return nil
	}
	if required != 0 || output != 0 {
		err := s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("ellin-reactor-%s-%d-%d", inst.id, oid, pq.reactorUses),
			func(session domain.CharacterSession) (domain.ScriptExchange, error) {
				if s.eventOf(session.CharacterID) != inst || session.Character.MapID != mapID || !time.Now().Before(inst.deadline) {
					return domain.ScriptExchange{}, domain.ErrStaleOwner
				}
				if _, _, stillReady := ellinClickEffect(reactorSnapshot, session.Character); !stillReady {
					return domain.ScriptExchange{}, domain.ErrStaleOwner
				}
				var trade domain.ScriptExchange
				if required != 0 {
					trade.Items = []domain.ScriptItemDelta{{TemplateID: required, Quantity: -1}}
				}
				if output != 0 {
					x, _ := ellinOutputPoint(def)
					trade.Drops = []domain.ScriptDrop{{TemplateID: output, Quantity: 1, X: x, Y: ground}}
				}
				return trade, nil
			})
		if err != nil {
			return eventPortalNotice(sink, "請走近並準備好需要的道具。")
		}
		pq.reactorUses++
	}
	g.mu.Lock()
	if g.rooms[key] != room {
		g.mu.Unlock()
		return nil
	}
	reactor.state++
	packets := roomBroadcast(room, protocol.ReactorState(oid, reactor.state, def.X, def.Y), -1)
	switch def.TemplateID {
	case 3002000:
		reactor.respawnAt = time.Now().Add(2 * time.Second)
	case 3001000:
		life := content.Life{Kind: "m", X: 392, Y: 149, Foothold: 10, RX0: -500, RX1: 975, RespawnSeconds: -1}
		form, spawn := g.spawnEventBossForm(room, life, pq.forms[0], actor.CharacterID, time.Now())
		if form != nil {
			room.bossForms = &eventBossForms{active: form, next: pq.forms[1:]}
			packets = append(packets, spawn...)
		}
	}
	g.mu.Unlock()
	deliver(packets)
	return nil
}

func ellinOutputPoint(def content.Reactor) (int, int) {
	if def.TemplateID == 3002000 {
		return def.X + 80, def.Y - 120
	}
	return def.X, def.Y - 36
}

func ellinClickEffect(r roomReactor, actor domain.Character) (required, output int, ready bool) {
	x, y := actor.X-r.def.X, actor.Y-r.def.Y
	switch r.def.TemplateID {
	case 3002000:
		required, output = content.EllinStoneShard, content.EllinDilutedPoison
		ready = r.state == 0 && x >= -120 && x <= 120 && y >= -90 && y <= 50
	case 3009000:
		required = content.EllinDilutedPoison
		ready = r.state < 4 && x >= -110 && x <= 110 && y >= -115 && y <= 112
	case 3002001:
		if r.state == 2 {
			output = content.EllinPurpleStone
		}
		ready = (r.state == 0 || r.state == 2) && x >= -145 && x <= 145 && y >= -115 && y <= 32
	case 3001000:
		required = content.EllinPurpleStone
		ready = r.state == 0 && x >= -120 && x <= 120 && y >= 130 && y <= 250
	}
	return required, output, ready && (required == 0 || henesysItemCount(actor, required) > 0)
}

func (s *Server) tickEllinPQ(now time.Time) {
	g := s.gameplay
	g.mu.Lock()
	var packets []roomDelivery
	for key, room := range g.rooms {
		if key.mapID != content.EllinBoulder {
			continue
		}
		for oid, reactor := range room.reactors {
			if reactor.def.TemplateID != 3002000 || reactor.state != 1 || reactor.respawnAt.IsZero() || now.Before(reactor.respawnAt) {
				continue
			}
			reactor.state = 0
			reactor.respawnAt = time.Time{}
			packets = append(packets, roomBroadcast(room, protocol.ReactorState(oid, 0, reactor.def.X, reactor.def.Y), -1)...)
		}
	}
	g.mu.Unlock()
	deliver(packets)
}
