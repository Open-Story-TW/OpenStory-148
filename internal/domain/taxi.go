package domain

const LithTaxiNPC = 1002007
const LithTaxiMap = 104000000
const LithTaxiCoupon = 4032313

// TaxiChoice is server-assigned travel metadata. The generic Lua exchange
// table cannot construct it; the dedicated, NPC-bound host resolves the fare.
type TaxiChoice struct {
	Choice        int   `json:"choice"`
	DisplayedFare int64 `json:"displayed_fare"`
}

type TaxiPlan struct {
	MapID     int
	Fare      int64
	UseCoupon bool
}

// LithTaxiPlan follows scripts/npc/1002007.js. Re-evaluate it after taking the
// character database lock; a displayed fare never authorizes a stale discount
// or silently consumes a newly acquired coupon.
func LithTaxiPlan(c Character, choice int, displayedFare int64) (TaxiPlan, error) {
	maps := [...]int{120000000, 102000000, 100000000, 103000000}
	fares := [...]int64{1200, 1000, 1000, 1200}
	if choice < 0 || choice >= len(maps) || displayedFare < 0 {
		return TaxiPlan{}, ErrInvalidInput
	}
	for _, item := range c.Items {
		if item.TemplateID == LithTaxiCoupon && item.Quantity > 0 {
			if choice != 2 || displayedFare != 0 {
				return TaxiPlan{}, ErrForbidden
			}
			return TaxiPlan{MapID: maps[2], UseCoupon: true}, nil
		}
	}
	fare := fares[choice]
	if c.Job == 0 || c.Job == 1000 || c.Job == 2000 {
		fare /= 10
	}
	if displayedFare != fare {
		return TaxiPlan{}, ErrForbidden
	}
	return TaxiPlan{MapID: maps[choice], Fare: fare}, nil
}
