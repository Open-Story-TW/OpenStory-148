package content

import (
	"fmt"
	"path/filepath"
	"sync"
)

const (
	ZakumMineName     = "ZakumMine"
	ZakumMineLobby    = 211042300
	ZakumMineMain     = 280010000
	ZakumMineExit     = 280090000
	ZakumOreChest     = 2112014
	ZakumMineKey      = 4001016
	ZakumMineDocument = 4001015
	ZakumRoughOre     = 4001018
	ZakumFireOre      = 4031061
)

var zakumMineMaps = []int{
	ZakumMineMain, 280010010, 280010011, 280010020, 280010030, 280010031,
	280010040, 280010041, 280010050, 280010060, 280010070, 280010071, 280010080,
	280010081, 280010090, 280010091, 280010100, 280010101, 280010110, 280010120,
	280010130, 280010140, 280010150, 280011000, 280011001, 280011002, 280011003,
	280011004, 280011005, 280011006,
}

func zakumMineEvent() Event {
	return Event{Name: ZakumMineName, LobbyMap: ZakumMineLobby, Maps: zakumMineMaps, ExitMap: ZakumMineExit,
		Seconds: 1800, PartyMin: 1, PartyMax: 6, Keep: 1, LevelMin: 50, LevelMax: 200}
}

func ZakumMineMap(mapID int) bool {
	e, ok := EventByMap(mapID)
	return ok && e.Name == ZakumMineName
}

func ZakumMineReactor(templateID int) bool {
	return templateID == 2110000 || templateID == 2111000 ||
		templateID >= 2112000 && templateID <= ZakumOreChest || templateID == 2118000 || templateID == 2118001
}

var zakumMineHitCounts sync.Map

// ZakumMineHitCount follows the local v148 reactor link to its hit states.
func ZakumMineHitCount(root string, templateID int) (int, error) {
	key := fmt.Sprintf("%s|%d", root, templateID)
	if hits, found := zakumMineHitCounts.Load(key); found {
		return hits.(int), nil
	}

	read := func(id int) (*wzNode, error) {
		return readWZ(filepath.Join(root, "Reactor.wz", fmt.Sprintf("%07d.img.xml", id)))
	}
	template, err := read(templateID)
	if err != nil {
		return 0, err
	}
	if link := template.child("info").intValue("link", 0); link != 0 {
		template, err = read(link)
		if err != nil {
			return 0, err
		}
	}
	for i := 0; i < 8; i++ {
		state := template.child(fmt.Sprint(i)).child("event").child("0")
		if state == nil || state.intValue("type", -1) != 0 || state.intValue("state", 0) != i+1 {
			zakumMineHitCounts.Store(key, i)
			return i, nil
		}
	}
	return 0, fmt.Errorf("invalid hit-state chain for reactor %d", templateID)
}
