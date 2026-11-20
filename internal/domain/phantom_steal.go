package domain

import (
	"context"
	"time"
)

const (
	// PhantomStolenCapacity is the v148 total stolen-skill slot count across
	// tiers 1..4 (4 + 4 + 3 + 2, CharacterData mask 0x20000000).
	PhantomStolenCapacity = 13

	PhantomImpeccableMemory1 = 24001001
	PhantomImpeccableMemory2 = 24101001
	PhantomImpeccableMemory3 = 24111001
	PhantomImpeccableMemory4 = 24121001
)

// PhantomBaseSkills lists the four Impeccable Memory slot skill IDs in exact
// v148 CharacterData mask 0x10000000 wire order.
var PhantomBaseSkills = [4]int{
	PhantomImpeccableMemory1,
	PhantomImpeccableMemory2,
	PhantomImpeccableMemory3,
	PhantomImpeccableMemory4,
}

// PhantomStolenSkill is one skill stolen into one of Phantom's 13 slots.
type PhantomStolenSkill struct {
	Tier        int `json:"tier"`
	Slot        int `json:"slot"`
	SkillID     int `json:"skill_id"`
	Level       int `json:"level"`
	MasterLevel int `json:"master_level,omitempty"`
}

// PhantomStealState holds Phantom's stolen-skill table (4+4+3+2 = 13 slots)
// and four Impeccable Memory chosen-skill bindings.
type PhantomStealState struct {
	Stolen [PhantomStolenCapacity]PhantomStolenSkill `json:"stolen,omitzero"`
	Chosen [4]int                                    `json:"chosen,omitzero"`
}

type PhantomStealRepository interface {
	SavePhantomStealState(ctx context.Context, session CharacterSession, state PhantomStealState) (Character, error)
}

// PhantomSlotCapacity matches v148 sub_4C7721 (4/4/3/2 for tiers 1..4).
func PhantomSlotCapacity(tier int) int {
	switch tier {
	case 1, 2:
		return 4
	case 3:
		return 3
	case 4:
		return 2
	default:
		return 0
	}
}

// PhantomSlotOffset maps (tier 1..4, slot 0..cap-1) to a flat index 0..12.
func PhantomSlotOffset(tier, slot int) (int, bool) {
	cap := PhantomSlotCapacity(tier)
	if cap == 0 || slot < 0 || slot >= cap {
		return 0, false
	}
	base := [5]int{0, 0, 4, 8, 11}[tier]
	return base + slot, true
}

// PhantomBaseTier maps Impeccable Memory I..IV to tier 1..4 (0 otherwise).
func PhantomBaseTier(baseSkillID int) int {
	for i, id := range PhantomBaseSkills {
		if id == baseSkillID {
			return i + 1
		}
	}
	return 0
}

// PhantomJobTier returns the Explorer advancement tier 1..4 for stealable
// Explorer jobs (100..522, excluding Dual Blade 43x, Cannoneer 501/53x,
// and regional 508/509/57x/58x/59x). Returns 0 for non-stealable jobs.
func PhantomJobTier(job int) int {
	if job < 100 || job >= 600 {
		return 0
	}
	if job == 501 || job == 508 || job == 509 {
		return 0
	}
	group := job / 10
	if group == 43 || group == 53 || group == 57 || group == 58 || group == 59 {
		return 0
	}
	if job%100 == 0 {
		return 1
	}
	tier := job%10 + 2
	if tier < 2 || tier > 4 {
		return 0
	}
	return tier
}

// PhantomSkillTier returns the stealable tier 1..4 of skillID (0 if not stealable).
func PhantomSkillTier(skillID int) int {
	if skillID <= 0 {
		return 0
	}
	return PhantomJobTier(skillID / 10000)
}

// nonStealableExplorerSkills mirrors the v148 exclusion list in sub_4E5A63.
var nonStealableExplorerSkills = map[int]bool{
	1121010: true, // Enrage
	1211002: true, // Charged Blow
	2121004: true, // Infinity (F/P)
	2221004: true, // Infinity (I/L)
	2321004: true, // Infinity (Bishop)
	3101004: true, // Soul Arrow: Bow
	3121007: true, // Illusion Step
	3201004: true, // Soul Arrow: Crossbow
	3221006: true, // Illusion Step (Marksman)
	4001011: true, // Flash Jump (Thief)
	4001013: true,
	4111002: true, // Shadow Partner (NL)
	4111009: true, // Shadow Stars
	4211008: true, // Shadow Partner (Shadower)
	5001005: true, // Dash
	5091001: true,
	5091005: true,
	5201008: true, // Infinity Blast
}

// IsStealableExplorerSkill reports whether a Phantom can steal skillID.
func IsStealableExplorerSkill(skillID int) bool {
	if skillID <= 0 || nonStealableExplorerSkills[skillID] {
		return false
	}
	// Active skills only (v148 sub_4C75E6: 4th digit is 1).
	if (skillID%10000)/1000 != 1 {
		return false
	}
	return PhantomSkillTier(skillID) > 0
}

// HasAny reports whether any stolen or chosen skill is stored.
func (p PhantomStealState) HasAny() bool {
	for _, s := range p.Stolen {
		if s.SkillID > 0 {
			return true
		}
	}
	for _, id := range p.Chosen {
		if id > 0 {
			return true
		}
	}
	return false
}

// FindStolen returns the flat index and entry for skillID if present.
func (p PhantomStealState) FindStolen(skillID int) (int, PhantomStolenSkill, bool) {
	if skillID <= 0 {
		return -1, PhantomStolenSkill{}, false
	}
	for i, s := range p.Stolen {
		if s.SkillID == skillID {
			return i, s, true
		}
	}
	return -1, PhantomStolenSkill{}, false
}

// ChosenStolenLevel returns the effective cast/attack level for a skill that
// Phantom currently has chosen in an Impeccable Memory slot, capped by the
// learned level of that Impeccable Memory base skill. Returns 0 otherwise.
func (c Character) ChosenStolenLevel(skillID int, now time.Time) int {
	if skillID <= 0 || !IsPhantomJob(c.Job) {
		return 0
	}
	for i, chosen := range c.PhantomSteal.Chosen {
		if chosen != skillID {
			continue
		}
		_, entry, ok := c.PhantomSteal.FindStolen(skillID)
		if !ok || entry.Level < 1 {
			continue
		}
		baseID := PhantomBaseSkills[i]
		baseLevel := 0
		for _, s := range c.Skills {
			if s.ID == baseID && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
				baseLevel = s.Level
				break
			}
		}
		if baseLevel < 1 {
			continue
		}
		return min(entry.Level, baseLevel)
	}
	return 0
}
