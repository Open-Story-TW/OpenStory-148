package content

import (
	"strconv"
	"strings"
)

// 武陵道場 Mu Lung Dojo, normal mode. The v148 Map.wz has floors 1-47 at
// 925020000 + floor*100, each with ten copies (+0..9). The copies are the
// instance rooms: one group holds the same room index on every floor. Floors
// 6, 12, ..., 42 have no field type and no boss; the other forty are
// CField_Dojang (fieldType 14) floors whose bosses are 9305100-9305139 in
// floor order.
const (
	DojoLobby  = 925020000 // 武陵道場 courtyard (board NPC destination).
	DojoHall   = 925020001 // 道場大廳: So Gong starts the challenge here.
	DojoExit   = 925020002 // forcedReturn of every floor.
	DojoNPC    = 2091005   // 蘇功 So Gong.
	DojoFloors = 47
	DojoRooms  = 10
	DojoStages = 40 // boss floors.

	DojoFirstBoss   = 9305100
	DojoDoorReactor = 2508000 // "door" on every boss floor; state 1 is open.

	// DojoElapsedRecord is the quest record whose integer value the client
	// adds to the elapsed time of the running floor (0x52C609, 0x52B3B7).
	DojoElapsedRecord = 7214

	// Energy: CWvsContext keeps the "energy" session value (0x9C2CAC) and the
	// key handler (0x4FE6EC) offers the three dojo skills at 10000.
	DojoEnergyFull = 10000
	// DojoEnergyPerHit is a server rule (the client only displays the gauge):
	// each damaging attack adds this much per dojo monster it hit, capped at
	// DojoEnergyFull. The v14x-era Legacy Java server (MapleCharacter
	// mulung_EnergyModify, called from MapleMonster.damage for HP display
	// type 2 dojo bosses) uses the same +100 per damaged boss and a reset to 0
	// on use.
	DojoEnergyPerHit = 100

	// DojoRestFloorSeconds only bounds an idle rest floor on the server: rest
	// floors have no dojang_Eff entry script in Map.wz, so they show no clock.
	DojoRestFloorSeconds = 600

	dojoEventPrefix = "MuLungDojo"
)

// DojoFloorSeconds is the clock of a boss floor. The v148 client has no
// default (CField_Dojang 0x52C609 shows the seconds of the server's 0xE5
// clock) and Map.wz has no timeLimit, so the time comes from the v14x-era
// Legacy Java server (MapScriptMethods getTiming, minutes by floor for its
// 38-floor tower): 5 for floors 1-5, 6 for 7-11, 7 for 13-17, 8 for 19-23,
// 9 for 25-29, 10 for 31-35 and 15 for 37-38. v148 adds floors 39-47; they
// keep the last tier's 15 minutes (not covered by that source).
func DojoFloorSeconds(floor int) int {
	minutes := 15
	switch {
	case floor < 1 || floor > DojoFloors:
		return 0
	case DojoRestFloor(floor):
		return DojoRestFloorSeconds
	case floor <= 5:
		minutes = 5
	case floor <= 11:
		minutes = 6
	case floor <= 17:
		minutes = 7
	case floor <= 23:
		minutes = 8
	case floor <= 29:
		minutes = 9
	case floor <= 35:
		minutes = 10
	}
	return minutes * 60
}

// DojoEventName is the event that holds instance room index room.
func DojoEventName(room int) string { return dojoEventPrefix + strconv.Itoa(room) }

// DojoEvent reports whether name is one of the dojo instance events.
func DojoEvent(name string) bool {
	_, ok := DojoEventRoom(name)
	return ok
}

// DojoEventRoom is the room index of a dojo instance event name.
func DojoEventRoom(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, dojoEventPrefix)
	if !ok || len(rest) != 1 || rest[0] < '0' || rest[0] > '9' {
		return 0, false
	}
	return int(rest[0] - '0'), true
}

// DojoFloorMap is the map of floor (1..47) in instance room (0..9).
func DojoFloorMap(floor, room int) int { return 925020000 + floor*100 + room }

// DojoFloorOf finds the floor and room of a normal-mode floor map.
func DojoFloorOf(mapID int) (floor, room int, ok bool) {
	offset := mapID - 925020000
	floor, room = offset/100, offset%100
	if offset < 0 || floor < 1 || floor > DojoFloors || room >= DojoRooms {
		return 0, 0, false
	}
	return floor, room, true
}

// DojoRestFloor reports the floors without a boss (no field type in Map.wz).
func DojoRestFloor(floor int) bool { return floor >= 1 && floor <= DojoFloors && floor%6 == 0 }

// DojoStage is the boss number of a floor, 1..40, the n of the
// Map.wz/Effect.img dojang/start/number/n effect; 0 on a rest floor.
func DojoStage(floor int) int {
	if floor < 1 || floor > DojoFloors || DojoRestFloor(floor) {
		return 0
	}
	return floor - floor/6
}

// DojoBoss is the boss template of a floor.
func DojoBoss(floor int) (int, bool) {
	stage := DojoStage(floor)
	if stage == 0 {
		return 0, false
	}
	return DojoFirstBoss + stage - 1, true
}

// DojoField reports any normal-mode floor map, rest floors included.
func DojoField(mapID int) bool {
	_, _, ok := DojoFloorOf(mapID)
	return ok
}

// DojangField reports a CField_Dojang floor (fieldType 14): the floors where
// the client offers the energy skills.
func DojangField(mapID int) bool {
	floor, _, ok := DojoFloorOf(mapID)
	return ok && !DojoRestFloor(floor)
}

// DojoReactor is the boss floor door the server opens on a clear.
func DojoReactor(mapID, template int) bool { return template == DojoDoorReactor && DojangField(mapID) }

// dojoBossSpawn stands the boss at x 140 on the floor's lower ground
// (foothold 6 runs from x -1 to 235 at y 7 on every floor): the spawn point
// the Legacy Java server (Event_DojoAgent point1) and a later TMS server
// (MapleDojoAgent point1) both use.
func dojoBossSpawn(floor, room int) (EventSpawn, bool) {
	boss, ok := DojoBoss(floor)
	if !ok {
		return EventSpawn{}, false
	}
	return EventSpawn{MapID: DojoFloorMap(floor, room), Life: Life{Kind: "m", TemplateID: boss,
		X: 140, Y: 7, Foothold: 6, RX0: 90, RX1: 190, RespawnSeconds: -1}}, true
}

// dojoEvent is one instance room: every floor of that room index. A solo
// character may start; a party leader brings the members on the hall map.
// The deadline is per floor (the world resets it on each floor).
func dojoEvent(room int) Event {
	e := Event{
		Name: DojoEventName(room), LobbyMap: DojoHall, ExitMap: DojoExit, Seconds: DojoFloorSeconds(1),
		PartyMin: 1, PartyMax: 6, Keep: 1, LevelMin: 1, LevelMax: 255, AllowSolo: true,
	}
	for floor := 1; floor <= DojoFloors; floor++ {
		e.Maps = append(e.Maps, DojoFloorMap(floor, room))
		if spawn, ok := dojoBossSpawn(floor, room); ok {
			e.Spawns = append(e.Spawns, spawn)
		}
	}
	return e
}
