package world

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
)

func TestTeslaTriangleFormsOnTheThirdCoilAndBreaksTogether(t *testing.T) {
	owner := &gameplayMember{sink: &gameplayTestSink{}}
	watcher := &gameplayTestSink{}
	room := &gameplayRoom{members: map[int64]*gameplayMember{1: owner, 2: {sink: watcher}}, summons: map[int32]*roomSummon{}}
	spec := &combat.SummonSpec{MoveAbility: 0, AssistType: 1, Multi: true}
	coil := func(oid int32, x int) *roomSummon {
		m := &roomSummon{objectID: oid, ownerID: 1, ownerEntry: owner, ownerLevel: 120, skillID: teslaCoil, source: teslaCoil, level: 20, x: x, y: 10, spec: spec, expiresAt: time.Now().Add(time.Minute)}
		room.summons[oid] = m
		return m
	}
	a := coil(1, -100)
	if out := formTeslaTriangleLocked(room, a); len(out) != 0 || a.teslaState != 0 {
		t.Fatal("one coil formed a triangle")
	}
	b := coil(2, 0)
	formTeslaTriangleLocked(room, b)
	c := coil(3, 100)
	out := formTeslaTriangleLocked(room, c)
	deliver(out)
	if c.teslaState != 1 || a.teslaState != 2 || b.teslaState != 2 {
		t.Fatalf("states %d %d %d", a.teslaState, b.teslaState, c.teslaState)
	}
	if c.teslaPoints != [3][2]int16{{-100, 10}, {0, 10}, {100, 10}} {
		t.Fatalf("triangle %v", c.teslaPoints)
	}
	// The two corners are created again (remove + create) for everyone.
	if watcher.count(protocol.SendRemoveSummon) != 2 || watcher.count(protocol.SendSpawnSummon) != 2 {
		t.Fatalf("corner re-creation remove=%d create=%d", watcher.count(protocol.SendRemoveSummon), watcher.count(protocol.SendSpawnSummon))
	}
	// One corner leaving ends the triangle.
	delete(room.summons, a.objectID)
	if out := breakTeslaTriangleLocked(room, a); len(out) == 0 || len(room.summons) != 0 {
		t.Fatalf("triangle left %d coils", len(room.summons))
	}
}

func TestTeslaSpawnPacketCarriesTheTriangle(t *testing.T) {
	base := protocol.SummonView{OwnerID: 1, ObjectID: 5, SkillID: teslaCoil, OwnerLevel: 120, SkillLevel: 20, Stance: 4}
	lone := protocol.SpawnSummon(base)
	corner := base
	corner.TeslaState = 2
	main := base
	main.TeslaState, main.TeslaPoints = 1, [3][2]int16{{-100, 10}, {0, 10}, {100, 10}}
	full := protocol.SpawnSummon(main)
	if len(protocol.SpawnSummon(corner)) != len(lone) || len(full) != len(lone)+12 || lone[len(lone)-1] != 0 || full[len(lone)-1] != 1 {
		t.Fatalf("tesla tails lone % x full % x", lone[len(lone)-3:], full[len(lone)-3:])
	}
}

func TestLiveOwnerSummonCountForTeslaCooldown(t *testing.T) {
	now := time.Now()
	s := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
		{channel: 1, mapID: 180000001}: {summons: map[int32]*roomSummon{
			1: {ownerID: 10, source: teslaCoil, expiresAt: now.Add(time.Minute)},
			2: {ownerID: 10, source: teslaCoil, expiresAt: now.Add(time.Minute)},
			3: {ownerID: 10, source: teslaCoil, expiresAt: now.Add(-time.Second)}, // expired
			4: {ownerID: 20, source: teslaCoil, expiresAt: now.Add(time.Minute)},  // other owner
			5: {ownerID: 10, source: 35121009, expiresAt: now.Add(time.Minute)},   // other skill
		}},
	}}}
	if got := s.liveOwnerSummonCount(1, 180000001, 10, teslaCoil, now); got != 2 {
		t.Fatalf("liveOwnerSummonCount = %d, want 2", got)
	}
}
