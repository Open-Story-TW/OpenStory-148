package domain

import "testing"

func TestLithTaxiFreshFareAndCouponPolicy(t *testing.T) {
	for _, job := range []int{0, 1000, 2000, 100, 200, 300, 400, 500} {
		for choice, fare := range []int64{1200, 1000, 1000, 1200} {
			if job == 0 || job == 1000 || job == 2000 {
				fare /= 10
			}
			p, err := LithTaxiPlan(Character{Job: job}, choice, fare)
			if err != nil || p.Fare != fare || p.UseCoupon {
				t.Fatalf("job%d choice%d: %+v %v", job, choice, p, err)
			}
			if _, err = LithTaxiPlan(Character{Job: job}, choice, fare+1); err == nil {
				t.Fatal("accepted changed fare")
			}
		}
	}
	c := Character{Job: 100, Items: []InventoryItem{{TemplateID: LithTaxiCoupon, Quantity: 1}}}
	p, err := LithTaxiPlan(c, 2, 0)
	if err != nil || !p.UseCoupon || p.MapID != 100000000 || p.Fare != 0 {
		t.Fatal(p, err)
	}
	for _, q := range [][2]int64{{-1, 0}, {4, 0}, {0, 0}, {2, 1000}} {
		if _, err = LithTaxiPlan(c, int(q[0]), q[1]); err == nil {
			t.Fatal("accepted", q)
		}
	}
	if _, err = LithTaxiPlan(Character{Job: 100}, 1, 100); err == nil {
		t.Fatal("stale beginner discount")
	}
	if _, err = LithTaxiPlan(Character{}, 2, 0); err == nil {
		t.Fatal("missing coupon")
	}
}
