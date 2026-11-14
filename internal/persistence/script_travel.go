package persistence

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func validateScriptTravel(request domain.ScriptExchange) error {
	t := request.Travel
	if t == nil {
		return nil
	}
	if t.FromMap != request.Session.Character.MapID || t.FromMap < 0 || t.MapID < 0 || t.MapID > 999999998 || t.SpawnPoint < 0 || t.SpawnPoint > 255 {
		return domain.ErrInvalidInput
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return err
	}
	m, err := catalog.Map(t.MapID)
	if err != nil {
		return domain.ErrInvalidInput
	}
	for _, p := range m.Portals {
		if p.ID == t.SpawnPoint && p.X == t.X && p.Y == t.Y {
			return nil
		}
	}
	return domain.ErrInvalidInput
}
