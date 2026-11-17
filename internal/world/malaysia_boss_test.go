package world

import (
	"testing"

	"maple-go-server/internal/content"
)

func TestMalaysiaStatuesReviveTheirOwnAttackableForms(t *testing.T) {
	definition, _ := content.EventByName(content.MalaysiaBossName)
	attempt := &eventInstance{definition: definition, members: map[int64]struct{}{1: {}, 2: {}}}
	key := mapKey{channel: 1, mapID: content.MalaysiaBossArena}
	room := &gameplayRoom{mobs: make(map[int32]*gameplayMob)}
	space := &gameplaySpace{rooms: map[mapKey]*gameplayRoom{key: room}}
	server := &Server{gameplay: space, events: newEventSpace()}
	if err := space.addMalaysiaStatues(room, key.mapID); err != nil {
		t.Fatal(err)
	}
	for _, statue := range room.malaysiaStatues {
		definition := statue.definition
		opener := space.newMalaysiaBoss(definition, content.Mob{ID: definition.FirstForm, HP: 10})
		body := space.newMalaysiaBoss(definition, content.Mob{ID: definition.FirstForm + 1, HP: 60_000_000})
		opener.fake = true
		statue.form = definition.FirstForm
		spawnMalaysiaBoss(room, opener)

		attempt.ended = true
		server.reviveMalaysiaBossOpener(key, room, statue, attempt, opener, body)
		if room.mobs[opener.objectID] != opener || room.mobs[body.objectID] != nil {
			t.Fatal("a callback after the attempt ended changed the field")
		}

		attempt.ended = false
		server.reviveMalaysiaBossOpener(key, room, statue, attempt, opener, body)
		server.reviveMalaysiaBossOpener(key, room, statue, attempt, opener, body)
		if statue.form != definition.FirstForm+1 || room.mobs[opener.objectID] != nil ||
			room.mobs[body.objectID] != body || body.fake || body.hp != 60_000_000 {
			t.Fatalf("statue %d failed to replace its opener with its first combat form", definition.Reactor)
		}
	}
	if len(room.mobs) != 2 {
		t.Fatalf("the two statues left %d mobs", len(room.mobs))
	}
}
