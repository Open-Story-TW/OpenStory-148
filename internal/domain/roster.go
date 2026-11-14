package domain

// RosterView keeps the scalar selection-screen fields and worn appearance.
// Dynamic gameplay collections never enter the login roster projection.
func RosterView(c Character) CharacterSummary {
	c.Items = nil
	c.Quests = nil
	c.QuestRecords = nil
	c.Skills = nil
	c.SkillCooldowns = nil
	c.SkillBuffs = nil
	c.Pets = nil
	c.InventorySlots = [5]int{}
	worn := make([]Equipment, 0, len(c.Equipment))
	for _, item := range c.Equipment {
		if item.Slot < 0 {
			worn = append(worn, item)
		}
	}
	c.Equipment = worn
	return c
}
