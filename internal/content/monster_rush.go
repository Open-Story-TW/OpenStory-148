package content

const (
	MonsterRushMob        = 3110300
	MonsterRushHP         = 800_000_000
	monsterRushExperience = 0
)

type MonsterRushAnchor struct {
	MapID, X, Y, Foothold int
}

func MonsterRushAnchors() [5]MonsterRushAnchor {
	return [5]MonsterRushAnchor{
		{260000000, 108, 275, 13}, {680000000, 2037, 193, 297},
		{211000000, -1463, 94, 156}, {600000000, 2031, 501, 210},
		{120000000, 1180, 305, 24},
	}
}

func MonsterRushMap(mapID int) bool {
	for _, anchor := range MonsterRushAnchors() {
		if anchor.MapID == mapID {
			return true
		}
	}
	return false
}

// Piggy2's spawnMrushMob1 overrides HP. Its OverrideMonsterStats constructor
// supplies zero for MP and EXP.
func MonsterRushStats(mob Mob) Mob {
	mob.HP = MonsterRushHP
	mob.MP = 0
	mob.EXP = monsterRushExperience
	return mob
}
