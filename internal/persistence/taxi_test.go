package persistence

import (
	"maple-go-server/internal/domain"
	"testing"
)

func TestTaxiExchangeRevalidatesFreshAssetsAndFare(t *testing.T) {
	c := domain.Character{MapID: domain.LithTaxiMap, HP: 50, Job: 0}
	base := domain.ScriptExchange{MesosDelta: -100, Travel: &domain.ScriptTravel{FromMap: domain.LithTaxiMap, MapID: 102000000, Taxi: &domain.TaxiChoice{Choice: 1, DisplayedFare: 100}}}
	if err := validateTaxiExchange(base, c); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"job", "dead", "map", "fare", "destination", "grant", "log"} {
		t.Run(name, func(t *testing.T) {
			q := base
			tr := *base.Travel
			q.Travel = &tr
			fresh := c
			switch name {
			case "job":
				fresh.Job = 100
			case "dead":
				fresh.HP = 0
			case "map":
				fresh.MapID++
			case "fare":
				q.MesosDelta = 0
			case "destination":
				tr.MapID = 100000000
			case "grant":
				q.Items = []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: 1}}
			case "log":
				q.Logs = []domain.ScriptLogChange{{}}
			}
			if err := validateTaxiExchange(q, fresh); err == nil {
				t.Fatal("accepted altered taxi")
			}
		})
	}
	c.Items = []domain.InventoryItem{{TemplateID: domain.LithTaxiCoupon, Quantity: 1}}
	q := domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: domain.LithTaxiCoupon, Quantity: -1}}, Travel: &domain.ScriptTravel{FromMap: domain.LithTaxiMap, MapID: 100000000, SpawnPoint: 6, Taxi: &domain.TaxiChoice{Choice: 2, DisplayedFare: 0}}}
	if err := validateTaxiExchange(q, c); err != nil {
		t.Fatal(err)
	}
	q.Travel.SpawnPoint = 0
	if err := validateTaxiExchange(q, c); err == nil {
		t.Fatal("accepted wrong coupon portal")
	}
}
