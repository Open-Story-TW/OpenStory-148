package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"math/big"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func applyQuestItem(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, item content.QuestItem) error {
	if item.ID/1000000 != 1 {
		return applyScriptItem(ctx, tx, result, domain.ScriptItemDelta{TemplateID: item.ID, Quantity: item.Count})
	}
	// Equipment grants use the same WZ base and positive-slot allocator as
	// NPC shops. Consuming equipped items or temporary/cash equipment is not
	// implied by this grant path and remains unsupported.
	definition, err := inventoryDefinition(item.ID)
	if err != nil || definition.InvType != 1 || definition.Cash || item.Count < 1 || item.Count > domain.MaxInventorySlots {
		return domain.ErrInvalidInput
	}
	for range item.Count {
		equipment := definition.Equipment
		equipment.Slot = FreeEquipmentSlot(&result.Character)
		if equipment.Slot == 0 {
			return domain.ErrInsufficient
		}
		if err = insertEquipment(ctx, tx, result.Character.ID, &equipment); err != nil {
			return err
		}
		result.Character.Equipment = append(result.Character.Equipment, equipment)
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: 1, Slot: equipment.Slot, Equipment: &equipment})
	}
	return nil
}

// This is the literal Legacy canGetItem job-family mapping. Unknown positive
// bits do not match any job; negative legacy masks bypass this filter, just
// as the Java `item.job > 0` condition does.
func eligibleQuestItem(item content.QuestItem, c domain.Character) bool {
	if item.Gender != nil && *item.Gender >= 0 && *item.Gender != 2 && *item.Gender != c.Gender {
		return false
	}
	if item.Job <= 0 {
		return true
	}
	family := c.Job / 100
	for _, mapping := range []struct {
		mask     int
		families []int
	}{
		{1, []int{0}}, {2, []int{1}}, {4, []int{2}}, {8, []int{3}}, {16, []int{4}}, {32, []int{5}},
		{0x400, []int{10}}, {0x800, []int{11}}, {0x1000, []int{12}}, {0x2000, []int{13}}, {0x4000, []int{14}}, {0x8000, []int{15}},
		{0x20000, []int{20, 22}}, {0x100000, []int{20}}, {0x200000, []int{21}}, {0x400000, []int{20, 22}}, {0x40000000, []int{30, 32, 33, 35}},
	} {
		if item.Job&mapping.mask != 0 {
			for _, job := range mapping.families {
				if family == job {
					return true
				}
			}
		}
	}
	if item.JobEx > 0 {
		for bit, job := range []int{2, 3, 4, 5} {
			if item.JobEx&(1<<bit) != 0 && family%10 == job {
				return true
			}
		}
	}
	return false
}

func randomQuestDraw(total int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(total))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}

// A single item ID is selected from the eligible weighted entries. Player
// choices instead use a zero-based index among eligible prop=-1 entries,
// matching Legacy canGetItem/extNum and the native CQuestMan choice menu.
// A missing or invalid choice rejects the whole transaction. The committed
// receipt stores the chosen inventory so replay never rerolls or reselects.
func chooseQuestItems(items []content.QuestItem, c domain.Character, selection int, draw func(int64) (int64, error)) ([]content.QuestItem, error) {
	var total int64
	choices := 0
	for _, item := range items {
		if eligibleQuestItem(item, c) && item.Weight != nil {
			if *item.Weight < -1 || *item.Weight > 1000000 {
				return nil, domain.ErrInvalidInput
			}
			if *item.Weight == -1 {
				choices++
			} else {
				total += int64(*item.Weight)
			}
		}
	}
	if choices > 0 && (selection < 0 || selection >= choices) || choices == 0 && selection != -1 && selection != 0 {
		return nil, domain.ErrInvalidInput
	}
	selected := 0
	if total > 0 {
		value, err := draw(total)
		if err != nil {
			return nil, err
		}
		if value < 0 || value >= total {
			return nil, domain.ErrInvalidInput
		}
		for _, item := range items {
			if item.Weight != nil && *item.Weight > 0 && eligibleQuestItem(item, c) {
				value -= int64(*item.Weight)
				if value < 0 {
					selected = item.ID
					break
				}
			}
		}
	}
	result := make([]content.QuestItem, 0, len(items))
	choiceIndex := 0
	for _, item := range items {
		if !eligibleQuestItem(item, c) {
			continue
		}
		if item.Weight != nil && *item.Weight == -1 {
			if choiceIndex == selection {
				result = append(result, item)
			}
			choiceIndex++
		} else if item.Weight == nil || *item.Weight > 0 && item.ID == selected {
			result = append(result, item)
		}
	}
	return result, nil
}
