package domain

import (
	"context"
	"time"
)

const (
	MaxSummonedPets   = 3
	PetMaxLevel       = 30
	PetMaxCloseness   = 30000
	PetMaxFullness    = 100
	PetFoodFullness   = 30
	PetHungerInterval = 150 * time.Second // Legacy world loop: 1.5 s x 100.
	FreeMarketMapID   = 910000000
)

// Legacy GameConstants.closeness; index level-1 is the total needed for level.
var petCloseness = [PetMaxLevel]int{0, 1, 3, 6, 14, 31, 60, 108, 181, 287, 434, 632, 891, 1224, 1642, 2161, 2793, 3557, 4467, 5542, 6801, 8263, 9950, 11882, 14084, 16578, 19391, 22547, 26074, 30000}

// PetLevelFor returns the level that a closeness total reaches.
func PetLevelFor(closeness int) int {
	level := 1
	for level < PetMaxLevel && closeness >= petCloseness[level] {
		level++
	}
	return level
}

func IsPetTemplate(id int) bool { return id/10000 == 500 }
func IsPetFood(id int) bool     { return id/10000 == 212 }

// Pet is durable pet state plus its CASH inventory location. X/Y/Stance and
// Foothold are field-local and are reset whenever the owner enters a field.
type Pet struct {
	CashID     int64  `json:"cash_id"`
	TemplateID int    `json:"template_id"`
	Slot       int    `json:"slot"`
	Name       string `json:"name"`
	Level      int    `json:"level"`
	Closeness  int    `json:"closeness"`
	Fullness   int    `json:"fullness"`
	Flags      int    `json:"flags"`
	SkillID    int    `json:"skill_id"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	// Summon is the native summoned value: 0 resting, 1..3 spawn index+1.
	Summon   int   `json:"summon,omitempty"`
	Revision int64 `json:"revision"`
	X        int   `json:"x,omitempty"`
	Y        int   `json:"y,omitempty"`
	Stance   int   `json:"stance,omitempty"`
	Foothold int   `json:"foothold,omitempty"`
}

func (p Pet) Summoned() bool { return p.Summon >= 1 && p.Summon <= MaxSummonedPets }
func (p Pet) Index() int     { return p.Summon - 1 }
func (p Pet) Expired(now time.Time) bool {
	return p.ExpiresAt > 0 && p.ExpiresAt <= now.UnixMilli()
}

// SummonedPets returns spawn-ordered summoned pets.
func (c Character) SummonedPets() []Pet {
	var out []Pet
	for index := 0; index < MaxSummonedPets; index++ {
		for _, p := range c.Pets {
			if p.Summon == index+1 {
				out = append(out, p)
			}
		}
	}
	return out
}

func (c Character) PetAt(index int) (Pet, bool) {
	for _, p := range c.Pets {
		if p.Summoned() && p.Index() == index {
			return p, true
		}
	}
	return Pet{}, false
}

// Pet mutators copy first: detached snapshots may share the backing array.

// PlacePetsAtOwner resets field-local pet coordinates for a new field visit.
func (c *Character) PlacePetsAtOwner() {
	c.Pets = append([]Pet(nil), c.Pets...)
	for i := range c.Pets {
		c.Pets[i].X, c.Pets[i].Y = c.X, c.Y-12
		c.Pets[i].Stance, c.Pets[i].Foothold = 0, 0
	}
}

// ReplacePet swaps one pet's durable state while retaining live coordinates.
func (c *Character) ReplacePet(next Pet) {
	c.Pets = append([]Pet(nil), c.Pets...)
	for i := range c.Pets {
		if c.Pets[i].CashID == next.CashID {
			next.X, next.Y, next.Stance, next.Foothold = c.Pets[i].X, c.Pets[i].Y, c.Pets[i].Stance, c.Pets[i].Foothold
			c.Pets[i] = next
			return
		}
	}
	c.Pets = append(c.Pets, next)
}

// MovePet updates one summoned pet's field-local position.
func (c *Character) MovePet(index, x, y int) bool {
	for i := range c.Pets {
		if c.Pets[i].Summoned() && c.Pets[i].Index() == index {
			c.Pets = append([]Pet(nil), c.Pets...)
			c.Pets[i].X, c.Pets[i].Y = x, y
			return true
		}
	}
	return false
}

// PetChange is one pet whose durable state changed. Reason is the native
// SPAWN_PET removal reason: 0 recalled, 1 hunger, 2 expired.
type PetChange struct {
	Before   Pet  `json:"before"`
	After    Pet  `json:"after"`
	Reason   int  `json:"reason,omitempty"`
	LevelUp  bool `json:"level_up,omitempty"`
	Recalled bool `json:"recalled,omitempty"`
}

type PetSummonRequest struct {
	Session     CharacterSession
	OperationID string
	Slot        int
	Lead        bool
}

type PetFeedRequest struct {
	Session     CharacterSession
	OperationID string
	ClientTick  uint32
	Slot        int
	TemplateID  int
	// Rolled by the world so that receipts replay one decision.
	GainCloseness bool
}

type PetCommandRequest struct {
	Session     CharacterSession
	OperationID string
	Index       int
	Increase    int
}

type PetDecayRequest struct {
	Session     CharacterSession
	OperationID string
	// Hunger per summoned cash serial; expired pets are recalled regardless.
	Hunger map[int64]int
	Now    time.Time
}

// PetResult carries the committed pets and, for food, the character whose
// USE inventory and revision changed in the same transaction.
type PetResult struct {
	Pets      []Pet       `json:"pets"`
	Changes   []PetChange `json:"changes,omitempty"`
	Character *Character  `json:"character,omitempty"`
	Success   bool        `json:"success"`
}

type PetGrantRequest struct {
	OperationID string `json:"operation_id"`
	AccountID   int64  `json:"account_id"`
	CharacterID int64  `json:"character_id"`
	TemplateID  int    `json:"template_id"`
	Name        string `json:"name"`
	PeriodDays  int    `json:"period_days"`
}

type PetRepository interface {
	SummonPet(context.Context, PetSummonRequest) (PetResult, error)
	FeedPet(context.Context, PetFeedRequest) (PetResult, error)
	CommandPet(context.Context, PetCommandRequest) (PetResult, error)
	DecayPets(context.Context, PetDecayRequest) (PetResult, error)
	GrantPet(context.Context, PetGrantRequest) (Pet, error)
	ListPets(ctx context.Context, accountID, characterID int64) ([]Pet, error)
}
