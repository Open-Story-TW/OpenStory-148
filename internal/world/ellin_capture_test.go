package world

import (
	"bytes"
	"context"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestEllinHealthyMonsterRefusalOmitsUnsupportedAnimation(t *testing.T) {
	const characterID, channel, objectID, slot = 27, 1, 1000001, 2
	actor := &liveCharacter{session: domain.CharacterSession{
		CharacterID: characterID,
		ChannelID:   channel,
		Character: domain.Character{
			ID: characterID, MapID: content.EllinCapture, HP: 100,
			Items: []domain.InventoryItem{{TemplateID: content.EllinPurifyMarble, InvType: 2, Slot: slot, Quantity: 10}},
		},
	}}
	mob := &gameplayMob{
		life:  content.Life{TemplateID: content.EllinPoisonMonster},
		stats: content.Mob{HP: 2640},
		hp:    2640,
	}
	sink := &gameplayTestSink{}
	room := &gameplayRoom{
		members: map[int64]*gameplayMember{characterID: {live: actor, sink: sink}},
		mobs:    map[int32]*gameplayMob{objectID: mob},
	}
	s := &Server{
		events: newEventSpace(),
		gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
			{channel: channel, mapID: content.EllinCapture}: room,
		}},
	}
	s.events.byMember[characterID] = &eventInstance{
		channel: channel, deadline: time.Now().Add(time.Minute), ellin: &ellinPQState{},
	}
	request := protocol.CaptureItemRequest{Slot: slot, ItemID: content.EllinPurifyMarble, ObjectID: objectID}
	if err := s.ellinCapture(context.Background(), channel, actor, sink, request); err != nil {
		t.Fatal(err)
	}
	if len(sink.packets) != 2 || !bytes.Equal(sink.packets[0], protocol.PortalTrainingMessage("請先將怪物的生命值降低到七成以下。")) ||
		!bytes.Equal(sink.packets[1], protocol.EnableActions()) {
		t.Fatalf("want refusal message and enabled actions, got %x", sink.packets)
	}
	if mob.hp != mob.stats.HP || mob.claiming || actor.session.Character.Items[0].Quantity != 10 {
		t.Fatal("refused capture changed the monster or consumed a marble")
	}
}
