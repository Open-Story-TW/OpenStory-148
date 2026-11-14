package content

import (
	"fmt"
	"path/filepath"

	"maple-go-server/internal/domain"
)

// QuestDropRemaining is deliberately stricter than Legacy's state-only spawn
// filter: this service exposes required collection items only while needed.
// Unknown WZ requirements never authorize an unconditional quest-item grant.
func QuestDropRemaining(root string, c domain.Character, questID, itemID int) (int, error) {
	active := false
	for _, q := range c.Quests {
		if q.ID == questID {
			active = q.State == 1
			break
		}
	}
	if !active {
		return 0, nil
	}
	q, err := QuestDefinition(root, questID)
	if err != nil {
		return 0, err
	}
	required := 0
	for _, i := range q.Complete.Items {
		if i.ID == itemID && i.Count > required {
			required = i.Count
		}
	}
	if required <= 0 {
		return 0, nil
	}
	owned := 0
	for _, i := range c.Items {
		if i.TemplateID == itemID {
			owned += i.Quantity
		}
	}
	return max(0, required-owned), nil
}

// MonsterQuestDropType preserves Java's explosive/public/party/owner ordering.
func MonsterQuestDropType(root string, mobID int, party bool) (int, error) {
	kind := 0
	if party {
		kind = 1
	}
	if root == "" {
		return kind, nil
	}
	n, err := readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", mobID)))
	if err != nil {
		return 0, err
	}
	info := n.child("info")
	if linked := info.intValue("link", 0); linked > 0 && linked != mobID {
		n, err = readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", linked)))
		if err != nil {
			return 0, err
		}
		info = n.child("info")
	}
	if info == nil {
		return 0, fmt.Errorf("monster has no drop ownership metadata")
	}
	if info.intValue("explosiveReward", 0) > 0 {
		return 3, nil
	}
	if info.intValue("publicReward", 0) > 0 {
		return 2, nil
	}
	return kind, nil
}
