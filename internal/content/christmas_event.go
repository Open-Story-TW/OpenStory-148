package content

import "strconv"

type ChristmasEvent struct {
	Event
	Snowman, Boss int
	RewardMap     int
	EXP, Credit   int64
}

var christmasEvents = func() [3]ChristmasEvent {
	var result [3]ChristmasEvent
	for mode, level := range [...]int{10, 30, 70} {
		lobby := 889100000 + 10*mode
		snowman := 9400326 + 5*mode
		e := Event{
			Name: "Christmas" + strconv.Itoa(mode), LobbyMap: lobby, Maps: []int{lobby + 1}, ExitMap: lobby, Seconds: 600,
			PartyMin: 2, PartyMax: 6, Keep: 2, LevelMin: level, LevelMax: 255,
			Spawns: []EventSpawn{{MapID: lobby + 1, Life: ChristmasMob(snowman)}},
		}
		result[mode] = ChristmasEvent{
			Event: e, Snowman: snowman, Boss: 9400319 + mode, RewardMap: lobby + 2,
			EXP: [...]int64{2500, 7500, 20000}[mode], Credit: [...]int64{250, 500, 750}[mode],
		}
	}
	return result
}()

func ChristmasByName(name string) (ChristmasEvent, bool) {
	for _, e := range christmasEvents {
		if e.Name == name {
			return e, true
		}
	}
	return ChristmasEvent{}, false
}

func ChristmasByMap(mapID int) (ChristmasEvent, bool) {
	for _, e := range christmasEvents {
		if e.Stage(mapID) {
			return e, true
		}
	}
	return ChristmasEvent{}, false
}

func ChristmasMob(template int) Life {
	return Life{Kind: "m", TemplateID: template, X: -189, Y: 34, Foothold: 24, RX0: -260, RX1: -90, RespawnSeconds: -1}
}

func (e ChristmasEvent) IsSnowman(template int) bool {
	return template >= e.Snowman-4 && template <= e.Snowman
}
