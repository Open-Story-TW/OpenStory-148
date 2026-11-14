package persistence

import "maple-go-server/internal/domain"

func validateTaxiExchange(r domain.ScriptExchange, fresh domain.Character) error {
	if r.Travel == nil || r.Travel.Taxi == nil {
		return nil
	}
	t := r.Travel
	if t.FromMap != domain.LithTaxiMap || fresh.MapID != domain.LithTaxiMap || fresh.HP <= 0 || len(r.Logs) != 0 || len(r.QuestData) != 0 {
		return domain.ErrForbidden
	}
	plan, err := domain.LithTaxiPlan(fresh, t.Taxi.Choice, t.Taxi.DisplayedFare)
	if err != nil {
		return err
	}
	if t.MapID != plan.MapID || r.MesosDelta != -plan.Fare {
		return domain.ErrForbidden
	}
	if plan.UseCoupon {
		if t.SpawnPoint != 6 || len(r.Items) != 1 || r.Items[0].TemplateID != domain.LithTaxiCoupon || r.Items[0].Quantity != -1 {
			return domain.ErrForbidden
		}
	} else if len(r.Items) != 0 {
		return domain.ErrForbidden
	}
	return nil
}
