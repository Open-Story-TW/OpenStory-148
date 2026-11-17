package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) ellinCapture(ctx context.Context, channel int, live *liveCharacter, sink packetSink, req protocol.CaptureItemRequest) error {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if req.ItemID != content.EllinPurifyMarble || actor.Character.MapID != content.EllinCapture ||
		actor.Character.HP <= 0 || inst == nil || inst.ellin == nil || inst.channel != channel || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	inst.ellin.mu.Lock()
	defer inst.ellin.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) ||
		!holdsCaptureMarble(actor.Character, req.Slot) {
		return sink.WritePacket(protocol.EnableActions())
	}
	g := s.gameplay
	key := mapKey{channel, content.EllinCapture}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[actor.CharacterID] == nil || !sameDropSink(room.members[actor.CharacterID].sink, sink) {
		g.mu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	mob := room.mobs[req.ObjectID]
	// The stock client selects the target with the marble's WZ capture rectangle.
	// A moving mob's position may differ across clients, so the server verifies
	// the actual object in the actor's instance and its authoritative HP.
	if mob == nil || mob.life.TemplateID != content.EllinPoisonMonster || mob.hp <= 0 || mob.claiming {
		g.mu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	if mob.hp*10 > mob.stats.HP*7 {
		g.mu.Unlock()
		// TWMS v148 loads ItemEff.img/2270004/fail for a false result.
		// The marble only has success frames, so report the refusal in text.
		return eventPortalNotice(sink, "請先將怪物的生命值降低到七成以下。")
	}
	mob.claiming = true
	g.mu.Unlock()
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("ellin-capture-%s-%d", inst.id, req.ObjectID),
		func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(session.CharacterID) != inst || session.Character.MapID != content.EllinCapture || !holdsCaptureMarble(session.Character, req.Slot) {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{Items: []domain.ScriptItemDelta{
				{TemplateID: content.EllinPurifyMarble, Quantity: -1},
				{TemplateID: content.EllinMonsterMarble, Quantity: 1},
			}}, nil
		})
	g.mu.Lock()
	mob.claiming = false
	var packets []roomDelivery
	if err == nil && g.rooms[key] == room && room.mobs[req.ObjectID] == mob {
		mob.hp = 0
		mob.respawnAt = mob.respawnTime(content.EllinCapture, time.Now())
		packets = roomBroadcast(room, protocol.CaptureMonsterResult(req.ObjectID, req.ItemID, true), -1)
		packets = append(packets, roomBroadcast(room, protocol.KillMonster(req.ObjectID), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
	if err != nil && !errors.Is(err, domain.ErrStaleOwner) && !errors.Is(err, domain.ErrInsufficient) {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func holdsCaptureMarble(c domain.Character, slot int) bool {
	for _, item := range c.Items {
		if item.InvType == 2 && item.Slot == slot && item.TemplateID == content.EllinPurifyMarble && item.Quantity > 0 {
			return true
		}
	}
	return false
}
