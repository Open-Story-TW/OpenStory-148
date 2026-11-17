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
	stage := &escortRoom{
		mob:       &gameplayMob{objectID: 1, hp: 100},
		stopUntil: deadline,
	}
	server := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
		{channel: 1, mapID: content.IceKnightFirst}: {
			escort:  stage,
			members: map[int64]*gameplayMember{1: {sink: sink}},
		},
	}}}
	server.tickEscortEvents(deadline.Add(-time.Millisecond))
	if len(sink.packets) != 0 || stage.stopUntil.IsZero() {
		t.Fatal("an escort resumed before its WZ stop elapsed")
	}
	server.tickEscortEvents(deadline)
	server.tickEscortEvents(deadline.Add(time.Millisecond))
	if len(sink.packets) != 1 || !bytes.Equal(sink.packets[0], protocol.ResumeEscort(1)) || !stage.stopUntil.IsZero() {
		t.Fatalf("escort resume packets = %x, remaining stop = %v", sink.packets, stage.stopUntil)
	}
}

func TestEscortNodeCanPrecedeMovementButCannotSkipStop(t *testing.T) {
	now := time.Now()
	mob := &gameplayMob{objectID: 1, hp: 80}
	escort := &escortRoom{mob: mob, next: 1}
	route := []content.EscortNode{{X: 0}, {X: 200, Attribute: 2, StopMillis: 2000}, {X: 400}, {X: 600}}
	room := &gameplayRoom{
		escort:     escort,
		definition: content.Map{EscortRoute: route},
		mobs:       map[int32]*gameplayMob{1: mob},
	}
	if escort.reportNode(3, route) {
		t.Fatal("node acknowledgement skipped a mandatory pause")
	}
	if !escort.reportNode(2, route) {
		t.Fatal("an early native acknowledgement should be retained")
	}
	advanceEscortLocked(room, now)
	if escort.next != 1 || escort.reported != 2 {
		t.Fatal("waypoint advanced before reaching its coordinates")
	}
	if applyMobMoveLocked(room, mob, protocol.MonsterMove{X: 200}) != mobMoveAccepted {
		t.Fatal("valid movement to the stop was rejected")
	}
	advanceEscortLocked(room, now)
	if escort.next != 2 || escort.stopUntil != now.Add(2*time.Second) || escort.reportNode(4, route) {
		t.Fatal("movement did not apply the pending stop")
	}
	if applyMobMoveLocked(room, mob, protocol.MonsterMove{X: 600}) != mobMoveRejected {
		t.Fatal("movement around an active stop was accepted")
	}
	escort.stopUntil = time.Time{}
	if !escort.reportNode(4, route) || applyMobMoveLocked(room, mob, protocol.MonsterMove{X: 600}) != mobMoveAccepted {
		t.Fatal("native movement could not catch up across an ordinary node")
	}
	advanceEscortLocked(room, now.Add(2*time.Second))
	if !escort.done || room.mobs[1] != nil {
		t.Fatal("escort did not clear at its final node")
	}
}

func TestIceKnightArenaWaitsForBothBossFormsAndEnding(t *testing.T) {
	now := time.Now()
	sink := &gameplayTestSink{}
	first := &gameplayMob{objectID: 1, hp: 50, life: content.Life{TemplateID: content.IceKnightBoss, RespawnSeconds: -1}}
	arena := &eventBossForms{active: first, next: []content.Mob{
		{ID: 9300442, HP: 75},
		{ID: 9300465, HP: 75, Invincible: true, RemoveAfter: 2},
	}}
	room := &gameplayRoom{
		bossForms: arena,
		mobs:      map[int32]*gameplayMob{1: first},
		members:   map[int64]*gameplayMember{1: {sink: sink}},
	}
	server := &Server{gameplay: &gameplaySpace{
		nextID: 100,
		rooms:  map[mapKey]*gameplayRoom{{channel: 1, mapID: content.IceKnightArena}: room},
	}}
	server.tickEscortEvents(now)
	if arena.active != first || arena.done {
		t.Fatal("first boss form skipped while alive")
	}
	first.hp = 0
	server.tickEscortEvents(now)
	second := arena.active
	if second == first || second.life.TemplateID != 9300442 || second.hp != 75 || arena.done {
		t.Fatal("first kill did not spawn the active Ice Knight")
	}
	second.hp = 0
	server.tickEscortEvents(now.Add(time.Second))
	ending := arena.active
	if ending.life.TemplateID != 9300465 || !ending.stats.Invincible || arena.done {
		t.Fatal("second kill did not start the temporary ending form")
	}
	server.tickEscortEvents(now.Add(3*time.Second - time.Millisecond))
	if arena.done || room.mobs[ending.objectID] != ending {
		t.Fatal("ending form expired early")
	}
	server.tickEscortEvents(now.Add(3 * time.Second))
	if !arena.done || room.mobs[ending.objectID] != nil {
		t.Fatal("arena did not clear after the ending form expired")
	}
}
