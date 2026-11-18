package domain

import "sort"

// ProfessionState is one learned profession (Skill.wz 9200-9204): its level
// and the proficiency gathered toward the next one.
type ProfessionState struct {
	ID    int `json:"id"`
	Level int `json:"level"`
	EXP   int `json:"exp"`
}

// Traits holds the experience of the six personality traits.
type Traits struct {
	Charisma int `json:"charisma,omitempty"`
	Insight  int `json:"insight,omitempty"`
	Will     int `json:"will,omitempty"`
	Craft    int `json:"craft,omitempty"`
	Sense    int `json:"sense,omitempty"`
	Charm    int `json:"charm,omitempty"`
}

// MaxTraitEXP is the experience of a trait at its last level.
const MaxTraitEXP = 93596

// Cumulative v148 trait EXP for levels 5, 10, ..., 100.
var willTierEXP = [...]int{
	181, 851, 2273, 4563, 7562, 11040, 14893, 19044, 23515, 28331,
	33519, 39110, 45135, 51626, 58371, 65216, 72161, 79206, 86351, MaxTraitEXP,
}

// WillTier returns the number of five-level Will milestones reached.
func (t Traits) WillTier() int {
	return sort.Search(len(willTierEXP), func(i int) bool { return t.Will < willTierEXP[i] })
}

// Add returns t plus d, each trait kept within 0..MaxTraitEXP.
func (t Traits) Add(d Traits) Traits {
	sum := func(a, b int) int { return max(0, min(MaxTraitEXP, a+b)) }
	return Traits{Charisma: sum(t.Charisma, d.Charisma), Insight: sum(t.Insight, d.Insight), Will: sum(t.Will, d.Will), Craft: sum(t.Craft, d.Craft), Sense: sum(t.Sense, d.Sense), Charm: sum(t.Charm, d.Charm)}
}

// Profession returns the character's state in a profession; Level is 0 when
// it has not been learned.
func (c Character) Profession(id int) ProfessionState {
	for _, p := range c.Professions {
		if p.ID == id {
			return p
		}
	}
	return ProfessionState{ID: id}
}

// KnowsRecipe reports whether a recipe that needs a recipe item was learned.
func (c Character) KnowsRecipe(id int) bool {
	for _, r := range c.Recipes {
		if r == id {
			return true
		}
	}
	return false
}

// ProfessionChange is the profession part of one script exchange. The host
// builds it from server rules; a script never supplies these fields raw.
type ProfessionChange struct {
	Learn   int    `json:"learn,omitempty"`   // profession to learn at level 1
	Forget  int    `json:"forget,omitempty"`  // profession dropped with its level and recipes
	Gain    int    `json:"gain,omitempty"`    // profession that receives EXP
	EXP     int    `json:"exp,omitempty"`     // proficiency added to Gain
	Fatigue int    `json:"fatigue,omitempty"` // fatigue added; refused past the limit
	Traits  Traits `json:"traits,omitzero"`   // trait experience added
	Recipe  int    `json:"recipe,omitempty"`  // recipe learned from a recipe item
}
