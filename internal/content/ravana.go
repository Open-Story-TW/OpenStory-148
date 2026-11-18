package content

const (
	RavanaName        = "Ravana"
	RavanaStoryName   = "RavanaStory"
	RavanaTemple      = 252000000
	RavanaAbandoned   = 252020000
	RavanaTrial       = 252020700
	RavanaLobby       = 252030000
	RavanaExit        = 252030001
	RavanaArena       = 252030100
	RavanaStoryArena  = 925120100
	RavanaFirstSpark  = 3862
	RavanaStoryQuest  = 3863
	RavanaEntryQuest  = 3864
	RavanaBurning     = 2430673
	RavanaSpark       = 4001684
	RavanaQuestSpark  = 4033176
	RavanaGoldenCrown = 1003455
)

func ravanaDropRows(mobID, mapID int) []DropDefinition {
	// The mounted client fixes the item templates and quest relationships.
	// The server supplies the chance values because WZ omits loot rates.
	drop := func(item, quest, chance int) DropDefinition {
		return DropDefinition{ID: item, MobID: mobID, TemplateID: item, Minimum: 1, Maximum: 1, QuestID: quest, Chance: chance}
	}
	if mapID == RavanaArena && mobID == 8800200 {
		return []DropDefinition{drop(RavanaGoldenCrown, 0, 200)}
	}
	if mapID < 252010000 || mapID > RavanaTrial || mobID < 8144000 || mobID > 8144008 {
		return nil
	}
	// The script's quest requirement gates crafting; the field loot is a
	// regular USE item. Quest 3864 asks for the finished Sunburst instead.
	rows := []DropDefinition{drop(RavanaBurning, 0, 20000)}
	if mapID >= 252020100 && mobID >= 8144004 {
		rows = append(rows, drop(RavanaQuestSpark, RavanaFirstSpark, 20000))
	}
	return rows
}

func ravanaEvent(story bool) Event {
	e := Event{
		Name: RavanaName, LobbyMap: RavanaLobby, Maps: []int{RavanaArena}, ExitMap: RavanaExit,
		Seconds: 1800, PartyMin: 2, PartyMax: 6, Keep: 1, LevelMin: 115, LevelMax: 255,
		RequiredMobs: map[int]map[int]int{RavanaArena: {8800200: 1}},
	}
	if story {
		e.Name, e.Maps = RavanaStoryName, []int{RavanaStoryArena}
		e.PartyMin, e.PartyMax = 1, 1
		e.RequiredMobs = map[int]map[int]int{RavanaStoryArena: {9100025: 1}}
	}
	return e
}
