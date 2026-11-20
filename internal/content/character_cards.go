package content

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
)

var characterCardSkills sync.Map

// CharacterCardSkill looks up a job's passive under Etc.wz/CharacterCard.img.
// The table uses the job group (job/10), including regional jobs such as 57.
func CharacterCardSkill(root string, job int) (int, error) {
	if root == "" {
		return 0, nil
	}
	table, ok := characterCardSkills.Load(root)
	if !ok {
		n, err := readWZ(filepath.Join(root, "Etc.wz", "CharacterCard.img.xml"))
		if err != nil {
			return 0, err
		}
		cards := n.child("Card")
		if cards == nil {
			return 0, fmt.Errorf("CharacterCard.img has no Card directory")
		}
		byJob := make(map[int]int, len(cards.Children))
		for i := range cards.Children {
			entry := &cards.Children[i]
			group, err := strconv.Atoi(entry.Name)
			skill := entry.intValue("skillID", 0)
			if err != nil || group < 1 || skill/10000 != 7100 || byJob[group] != 0 {
				return 0, fmt.Errorf("invalid character card entry %q", entry.Name)
			}
			byJob[group] = skill
		}
		table, _ = characterCardSkills.LoadOrStore(root, byJob)
	}
	return table.(map[int]int)[job/10], nil
}
