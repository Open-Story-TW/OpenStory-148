package domain

import "context"

const CharacterCardsPerDeck = 3

type CharacterCard struct {
	CharacterID int64 `json:"character_id"`
	Level       int   `json:"level"`
	Job         int   `json:"job"`
}

type CharacterCards [2 * CharacterCardsPerDeck]CharacterCard
type CharacterCardIDs [len(CharacterCards{})]int64

type CharacterCardRepository interface {
	LoadCharacterCards(ctx context.Context, accountID int64, worldID int) (CharacterCards, error)
	SaveCharacterCards(ctx context.Context, session LoginSession, worldID int, ids CharacterCardIDs) error
}

// Rank matches the original roster card eligibility and four level thresholds.
func (c CharacterCard) Rank() int {
	if c.CharacterID <= 0 || !ValidJob(c.Job) || c.Level < 30 || c.Job%100 == 0 {
		return 0
	}
	switch c.Job {
	case 501, 508, 509, 2001, 2002, 2003, 3001:
		return 0
	}
	tier := c.Job%10 + 2
	switch {
	case c.Job/10 == 43:
		if tier < 3 || tier > 6 {
			return 0
		}
	case c.Job/100 == 22:
		if tier < 3 || tier > 10 {
			return 0
		}
	default:
		if tier > 4 {
			return 0
		}
	}
	switch {
	case c.Job/1000 == 1 && c.Level >= 70:
		return 2
	case c.Level >= 200:
		return 4
	case c.Level >= 120:
		return 3
	case c.Level >= 70:
		return 2
	default:
		return 1
	}
}

func ValidateCharacterCards(cards CharacterCards) error {
	seen := map[int64]bool{}
	jobs := [2]map[int]bool{{}, {}}
	for slot, card := range cards {
		if card == (CharacterCard{}) {
			continue
		}
		deck, job := slot/CharacterCardsPerDeck, card.Job/10
		if card.Rank() == 0 || seen[card.CharacterID] || jobs[deck][job] {
			return ErrInvalidInput
		}
		seen[card.CharacterID], jobs[deck][job] = true, true
	}
	return nil
}
