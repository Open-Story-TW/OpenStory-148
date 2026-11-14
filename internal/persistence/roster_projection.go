package persistence

import (
	"encoding/json"

	"maple-go-server/internal/domain"
)

func marshalRoster(c domain.Character) ([]byte, error) {
	return json.Marshal(domain.RosterView(c))
}

func rosterViews(values []domain.CharacterSummary) []domain.CharacterSummary {
	out := make([]domain.CharacterSummary, 0, min(len(values), domain.MaxCharacterSlots))
	for _, c := range values[:min(len(values), domain.MaxCharacterSlots)] {
		out = append(out, domain.RosterView(c))
	}
	return out
}
