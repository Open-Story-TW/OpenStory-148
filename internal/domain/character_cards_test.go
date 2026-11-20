package domain

import "testing"

func TestV148CharacterCardRosterRestrictions(t *testing.T) {
	for _, tt := range []struct{ job, level, rank int }{
		{112, 29, 0}, {112, 30, 1}, {112, 70, 2}, {112, 120, 3}, {112, 200, 4},
		{1311, 120, 2}, {2001, 200, 0}, {2210, 200, 0}, {2211, 200, 4},
		{430, 200, 0}, {431, 200, 4}, {5111, 120, 3},
	} {
		card := CharacterCard{CharacterID: 1, Level: tt.level, Job: tt.job}
		if got := card.Rank(); got != tt.rank {
			t.Errorf("job %d level %d rank %d, want %d", tt.job, tt.level, got, tt.rank)
		}
	}
	hero := CharacterCard{CharacterID: 1, Level: 120, Job: 112}
	otherHero := CharacterCard{CharacterID: 2, Level: 70, Job: 111}
	if err := ValidateCharacterCards(CharacterCards{hero, {}, {}, otherHero}); err != nil {
		t.Fatalf("same job class in separate decks: %v", err)
	}
	for _, invalid := range []CharacterCards{{hero, otherHero}, {hero, {}, {}, hero}} {
		if err := ValidateCharacterCards(invalid); err == nil {
			t.Error("accepted duplicate job in one deck or duplicate avatar between decks")
		}
	}
}
