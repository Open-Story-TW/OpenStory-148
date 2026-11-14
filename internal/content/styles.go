package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

var styleCache sync.Map

// StyleExists reports whether the game data has this hair (3xxxx/4xxxx) or
// face (2xxxx) as Character.wz/Hair or Face/<id>.img. The client cannot draw
// a style it lacks, so beauty NPCs offer only these. Without a WZ root every
// ID in range is accepted.
func StyleExists(root string, id int) bool {
	var dir string
	switch {
	case id >= 20000 && id <= 29999:
		dir = "Face"
	case id >= 30000 && id <= 49999:
		dir = "Hair"
	default:
		return false
	}
	if root == "" {
		return true
	}
	key := root + "\x00" + strconv.Itoa(id)
	if known, ok := styleCache.Load(key); ok {
		return known.(bool)
	}
	_, err := os.Stat(filepath.Join(root, "Character.wz", dir, fmt.Sprintf("%08d.img.xml", id)))
	styleCache.Store(key, err == nil)
	return err == nil
}

type npcNames struct {
	once sync.Once
	byID map[int]string
}

var npcNameCache sync.Map

// NPCName is the NPC's name from String.wz/Npc.img; ok is false without one.
func NPCName(root string, id int) (string, bool) {
	value, _ := npcNameCache.LoadOrStore(root, &npcNames{})
	names := value.(*npcNames)
	names.once.Do(func() {
		names.byID = map[int]string{}
		if root == "" {
			return
		}
		node, err := readWZ(filepath.Join(root, "String.wz", "Npc.img.xml"))
		if err != nil {
			return
		}
		for i := range node.Children {
			n := &node.Children[i]
			if npc, err := strconv.Atoi(n.Name); err == nil {
				if name := n.stringValue("name", ""); name != "" {
					names.byID[npc] = name
				}
			}
		}
	})
	name, ok := names.byID[id]
	return name, ok
}
