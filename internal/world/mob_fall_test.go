package world

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func enterMobFallRoom(t *testing.T) (*Server, *gameplayRoom, *gameplayMob, *liveCharacter, *gameplayTestSink) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: g, online: make(map[int64]domain.CharacterSession)}
	p := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, SessionID: "mob-fall", Generation: 1, Character: domain.Character{ID: 1, MapID: 40000, HP: 50, MaxHP: 50, Revision: 1}}}
	sink := &gameplayTestSink{}
	if err = s.enterGameplay(1, p, sink); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel: 1, mapID: 40000}]
	var mob *gameplayMob
	for _, m := range room.mobs {
		if mob == nil || m.objectID < mob.objectID {
			mob = m
		}
	}
	if mob == nil {
		t.Fatal("map 40000 spawned no mob")
	}
	return s, room, mob, p, sink
}

func TestFieldFloorIsLowestFoothold(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	m, err := g.catalog.Map(40000)
	if err != nil {
		t.Fatal(err)
	}
	floor, ok := fieldFloor(m)
	if !ok || floor != 215 {
		t.Fatalf("floor=%d ok=%v, want 215", floor, ok)
	}
	m.Footholds = nil
	if _, ok = fieldFloor(m); ok {
		t.Fatal("floor reported for a field without footholds")
	}
}

// Java stores a mob one pixel above the reported landing so a later entrant's
// spawn packet never puts it under the slope it stands on.
func TestMobMoveStoresOnePixelAboveReport(t *testing.T) {
	_, room, mob, _, _ := enterMobFallRoom(t)
	origin := mob.origin
	m := protocol.MonsterMove{ObjectID: mob.objectID, X: origin.X + 20, Y: origin.Y}
	if v := applyMobMoveLocked(room, mob, m); v != mobMoveAccepted {
		t.Fatalf("verdict=%d", v)
	}
	if mob.life.X != origin.X+20 || mob.life.Y != origin.Y-1 {
		t.Fatalf("stored (%d,%d), want (%d,%d)", mob.life.X, mob.life.Y, origin.X+20, origin.Y-1)
	}
	if mob.origin != origin {
		t.Fatal("origin changed")
	}
	// A teleport beyond the patrol clamp is answered but not stored.
	if v := applyMobMoveLocked(room, mob, protocol.MonsterMove{ObjectID: mob.objectID, X: origin.RX1 + 500, Y: origin.Y}); v != mobMoveRejected {
		t.Fatalf("verdict=%d", v)
	}
	if mob.life.X != origin.X+20 {
		t.Fatal("rejected move was stored")
	}
}

func TestMobReportedBelowFieldIsReseatedAtOrigin(t *testing.T) {
	s, room, mob, _, sink := enterMobFallRoom(t)
	g := s.gameplay
	oldID := mob.objectID
	g.mu.Lock()
	mob.life.X, mob.life.Y = mob.origin.X+30, mob.origin.Y+150 // latched a mid-fall report
	mob.hp = 1
	v := applyMobMoveLocked(room, mob, protocol.MonsterMove{ObjectID: oldID, X: mob.origin.X + 30, Y: 215 + mobFallSlack + 1})
	if v != mobMoveFellOut {
		g.mu.Unlock()
		t.Fatalf("verdict=%d", v)
	}
	out := g.reseatFallenMobLocked(room, mob)
	g.mu.Unlock()
	for _, d := range out {
		_ = d.sink.WritePacket(d.packet)
	}
	if mob.objectID == oldID || room.mobs[oldID] != nil || room.mobs[mob.objectID] != mob {
		t.Fatalf("mob keeps old id %d (new %d)", oldID, mob.objectID)
	}
	if mob.life != mob.origin || mob.hp != mob.stats.HP || mob.controller != 1 {
		t.Fatalf("not re-seated at origin: life=%+v hp=%d controller=%d", mob.life, mob.hp, mob.controller)
	}
	// One removal of the old object (no death animation), then spawn + control for the only member.
	var removed []byte
	for _, p := range sink.packets {
		if binary.LittleEndian.Uint16(p) == protocol.SendKillMonster {
			removed = p
		}
	}
	if removed == nil || int32(binary.LittleEndian.Uint32(removed[2:])) != oldID || removed[6] != 0 {
		t.Fatalf("remove packet %x", removed)
	}
	// Entry spawned every mob once; the re-seat adds one spawn and one control packet.
	if sink.count(protocol.SendSpawnMonster) != len(room.mobs)+1 || sink.count(protocol.SendControlMonster) != len(room.mobs)+1 {
		t.Fatalf("spawn=%d control=%d mobs=%d", sink.count(protocol.SendSpawnMonster), sink.count(protocol.SendControlMonster), len(room.mobs))
	}
}

// A mob inside a combat settlement keeps its identity; the re-seat waits for
// the next movement report.
func TestMobBelowFieldInSettlementIsLeftAlone(t *testing.T) {
	s, room, mob, _, _ := enterMobFallRoom(t)
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	oldID := mob.objectID
	mob.claiming = true
	if out := g.reseatFallenMobLocked(room, mob); out != nil || mob.objectID != oldID {
		t.Fatalf("claiming mob re-seated: out=%d id=%d", len(out), mob.objectID)
	}
	mob.claiming = false
	mob.reservation = &combatTicket{}
	if out := g.reseatFallenMobLocked(room, mob); out != nil || mob.objectID != oldID {
		t.Fatalf("reserved mob re-seated: out=%d id=%d", len(out), mob.objectID)
	}
}

func TestKentaSwimmingMobsUseFieldBounds(t *testing.T) {
	room := &gameplayRoom{definition: content.Map{
		ID: content.KentaRescue,
		Footholds: []content.Foothold{
			{X1: -700, Y1: 150, X2: 400, Y2: 300},
			{X1: -700, Y1: 1800, X2: 400, Y2: 1800},
		},
	}}
	origin := content.Life{X: -250, Y: 1100, RX0: -280, RX1: -220}
	mob := &gameplayMob{origin: origin, life: content.Life{X: -180, Y: 750}, stats: content.Mob{Flying: true}}
	if verdict := applyMobMoveLocked(room, mob, protocol.MonsterMove{X: 1, Y: 520}); verdict != mobMoveAccepted || mob.life.Y != 519 {
		t.Fatalf("native swimming path outside the spawn patrol was not stored: verdict=%v life=%+v", verdict, mob.life)
	}
	if verdict := applyMobMoveLocked(room, mob, protocol.MonsterMove{X: -65, Y: 2000}); verdict != mobMoveFellOut {
		t.Fatalf("out-of-bounds mob did not request recovery: %v", verdict)
	}
	if verdict := applyMobMoveLocked(room, mob, protocol.MonsterMove{X: -499, Y: 300}); verdict != mobMoveRejected {
		t.Fatalf("swimming movement accepted an implausible step: %v", verdict)
	}
}
