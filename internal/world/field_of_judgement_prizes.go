package world

import (
	"fmt"
	"math/rand/v2"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func judgementPrizePools(c *content.Catalog) ([2][7][]domain.ScriptDrop, error) {
	var groups [2][7][]domain.ScriptDrop
	var cosmetics, stars []int
	for id := 1702000; id <= 1702175; id++ {
		cosmetics = append(cosmetics, id)
	}
	for id := 2070003; id <= 2070016; id++ {
		stars = append(stars, id)
	}
	collect := func(ids []int, randomized bool) []domain.ScriptDrop {
		var pool []domain.ScriptDrop
		for _, id := range ids {
			def, err := c.Item(id)
			if err != nil || def.Quest || def.Cash && id/10000 != 170 {
				continue
			}
			quantity := 1
			if id == 2022273 || id == 2022245 {
				quantity = 3
			}
			pool = append(pool, domain.ScriptDrop{TemplateID: id, Quantity: quantity, RollEquipment: randomized})
		}
		return pool
	}
	cashWeapons := collect(cosmetics, false)
	potions := collect([]int{2022273, 2022245, 2022179}, false)
	throwingStars := collect(stars, false)
	regular := collect(judgementScrolls, false)
	middle := collect(judgementMiddleScrolls, false)
	for tier := range groups {
		groups[tier] = [7][]domain.ScriptDrop{
			cashWeapons,
			collect(judgementMeleePrizes[tier], true),
			collect(judgementRareScrolls[tier], false),
			potions, throwingStars, regular, middle,
		}
		for index, pool := range groups[tier] {
			if len(pool) == 0 {
				return groups, fmt.Errorf("Field of Judgement prize group %d/%d has no v148 items", tier, index)
			}
		}
	}
	return groups, nil
}

func (schedule *judgementSchedule) rollPrize(room, roll int) domain.ScriptDrop {
	tier := 0
	if room >= content.JudgementRoomFirst+2 {
		tier = 1
	}
	var category int
	switch {
	case roll <= 35:
		category = 0
	case roll <= 37:
		category = 1
	case roll <= 45:
		category = 2
	case roll <= 70:
		category = 3
	case roll <= 75:
		category = 4
	case roll <= 95:
		category = 5
	default:
		category = 6
	}
	pool := schedule.prizes[tier][category]
	return pool[rand.IntN(len(pool))]
}
