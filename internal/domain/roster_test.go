package domain

import "testing"

func TestRosterViewKeepsAppearanceWithoutGameplayCollections(t *testing.T) {
	c := Character{ID: 7, Name: "Roster", Level: 12, HP: 100, MP: 50, InventorySlots: [5]int{28, 32, 24, 28, 24},
		Equipment: []Equipment{{ID: 1, ItemID: 1322005, Slot: -11}, {ID: 2, ItemID: 1040010, Slot: 1}, {ID: 3, ItemID: 1002000, Slot: -101}},
		Items:     make([]InventoryItem, 1000), Quests: make([]QuestState, 1000), Skills: make([]SkillState, 1000), SkillBuffs: make([]SkillBuff, 1000), SkillCooldowns: make([]SkillCooldown, 1000)}
	summary := RosterView(c)
	if summary.ID != c.ID || summary.Name != c.Name || summary.Level != c.Level || summary.HP != c.HP || len(summary.Equipment) != 2 {
		t.Fatal("roster lost the selection screen appearance/stats")
	}
	if len(summary.Items)+len(summary.Quests)+len(summary.Skills)+len(summary.SkillBuffs)+len(summary.SkillCooldowns) != 0 {
		t.Fatal("roster retained dynamic gameplay data")
	}
	if summary.InventorySlots != [5]int{} || c.InventorySlots[0] != 28 {
		t.Fatal("inventory capacities leaked into roster or changed on character")
	}
	summary.Equipment[0].ItemID = 9999999
	if c.Equipment[0].ItemID != 1322005 || len(c.Items) != 1000 || len(c.Quests) != 1000 {
		t.Fatal("roster projection modified the authoritative character")
	}
}
