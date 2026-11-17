package world

import (
	"bytes"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

func TestIceEscortResumesAtWZDeadline(t *testing.T) {
	sink := &gameplayTestSink{}
	deadline := time.Now()
	stage := &iceEscortRoom{
		mob:       &gameplayMob{objectID: 1, hp: 100},
		stopUntil: deadline,
	}
	server := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
		{channel: 1, mapID: content.IceKnightFirst}: {
			ice:     stage,
			members: map[int64]*gameplayMember{1: {sink: sink}},
		},
	}}}
	server.tickIceKnight(deadline.Add(-time.Millisecond))
	if len(sink.packets) != 0 || stage.stopUntil.IsZero() {
		t.Fatal("an escort resumed before its WZ stop elapsed")
	}
	server.tickIceKnight(deadline)
	server.tickIceKnight(deadline.Add(time.Millisecond))
	if len(sink.packets) != 1 || !bytes.Equal(sink.packets[0], protocol.ResumeEscort(1)) || !stage.stopUntil.IsZero() {
		t.Fatalf("escort resume packets = %x, remaining stop = %v", sink.packets, stage.stopUntil)
	}
}

func TestIceKnightArenaWaitsForBothBossFormsAndEnding(t *testing.T) {
	now := time.Now()
	sink := &gameplayTestSink{}
	first := &gameplayMob{objectID: 1, hp: 50, life: content.Life{TemplateID: content.IceKnightBoss, RespawnSeconds: -1}}
	arena := &iceKnightArena{active: first, next: []content.Mob{
		{ID: 9300442, HP: 75},
		{ID: 9300465, HP: 75, Invincible: true, RemoveAfter: 2},
	}}
	room := &gameplayRoom{
		iceBoss: arena,
		mobs:    map[int32]*gameplayMob{1: first},
		members: map[int64]*gameplayMember{1: {sink: sink}},
	}
	server := &Server{gameplay: &gameplaySpace{
		nextID: 100,
		rooms:  map[mapKey]*gameplayRoom{{channel: 1, mapID: content.IceKnightArena}: room},
	}}
	server.tickIceKnight(now)
	if arena.active != first || arena.done {
		t.Fatal("first boss form skipped while alive")
	}
	first.hp = 0
	server.tickIceKnight(now)
	second := arena.active
	if second == first || second.life.TemplateID != 9300442 || second.hp != 75 || arena.done {
		t.Fatal("first kill did not spawn the active Ice Knight")
	}
	second.hp = 0
	server.tickIceKnight(now.Add(time.Second))
	ending := arena.active
	if ending.life.TemplateID != 9300465 || !ending.stats.Invincible || arena.done {
		t.Fatal("second kill did not start the temporary ending form")
	}
	server.tickIceKnight(now.Add(3*time.Second - time.Millisecond))
	if arena.done || room.mobs[ending.objectID] != ending {
		t.Fatal("ending form expired early")
	}
	server.tickIceKnight(now.Add(3 * time.Second))
	if !arena.done || room.mobs[ending.objectID] != nil {
		t.Fatal("arena did not clear after the ending form expired")
	}
}
