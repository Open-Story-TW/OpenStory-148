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

	// DojoFloorSeconds is the clock each floor gets. The v148 client has no
	// default: CField_Dojang (0x52C609) shows the seconds of the 0xE5 clock
	// the server sends, and Map.wz has no timeLimit on these floors.
	DojoFloorSeconds = 600
	// DojoElapsedRecord is the quest record whose integer value the client
	// adds to the elapsed time of the running floor (0x52C609, 0x52B3B7).
	DojoElapsedRecord = 7214

	// Energy: CWvsContext keeps the "energy" session value (0x9C2CAC) and the
	// key handler (0x4FE6EC) offers the three dojo skills at 10000.
	DojoEnergyFull = 10000
	// DojoEnergyPerHit is the server's own rule: the client only displays the
	// value. Every attack that damages a monster on a dojo floor adds this
	// much, so a full gauge takes 100 damaging attacks.
	DojoEnergyPerHit = 100

	dojoEventPrefix = "MuLungDojo"
)

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

// dojoBossSpawn stands the boss on the right half of the floor's lower
// ground (Map.wz foothold 7 runs from x 235 to 502 at y 7 on every floor),
// away from the six sp portals on the left.
func dojoBossSpawn(floor, room int) (EventSpawn, bool) {
	boss, ok := DojoBoss(floor)
	if !ok {
		return EventSpawn{}, false
	}
	return EventSpawn{MapID: DojoFloorMap(floor, room), Life: Life{Kind: "m", TemplateID: boss,
		X: 300, Y: 7, Foothold: 7, RX0: 250, RX1: 450, RespawnSeconds: -1}}, true
}

// dojoEvent is one instance room: every floor of that room index. A solo
// character may start; a party leader brings the members on the hall map.
// The deadline is per floor (the world resets it on each floor).
func dojoEvent(room int) Event {
	e := Event{
		Name: DojoEventName(room), LobbyMap: DojoHall, ExitMap: DojoExit, Seconds: DojoFloorSeconds,
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
