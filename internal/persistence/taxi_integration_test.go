package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"sync"
	"testing"
)

func TestTaxiAtomicFareCouponReplayAndIsolation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for i := range sessions {
		if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET map_id=$1,job=$2 WHERE id=$3`, domain.LithTaxiMap, 100, sessions[i].CharacterID); err != nil {
			t.Fatal(err)
		}
		sessions[i].Character, err = s.GetCharacter(ctx, sessions[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
	}
	makeRequest := func(i, choice int, fare int64, coupon bool) domain.ScriptExchange {
		t.Helper()
		plan, err := domain.LithTaxiPlan(sessions[i].Character, choice, fare)
		if err != nil {
			t.Fatal(err)
		}
		m, err := catalog.Map(plan.MapID)
		if err != nil {
			t.Fatal(err)
		}
		var p content.Portal
		found := false
		for _, candidate := range m.Portals {
			if candidate.ID == 0 && !coupon || candidate.ID == 6 && coupon {
				p = candidate
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing taxi destination portal", m.ID, coupon)
		}
		q := domain.ScriptExchange{Session: sessions[i], OperationID: fmt.Sprintf("taxi-operation-%d", sessions[i].CharacterID), MesosDelta: -fare, Travel: &domain.ScriptTravel{FromMap: domain.LithTaxiMap, MapID: plan.MapID, SpawnPoint: p.ID, X: p.X, Y: p.Y, Taxi: &domain.TaxiChoice{Choice: choice, DisplayedFare: fare}}}
		if coupon {
			q.Items = []domain.ScriptItemDelta{{TemplateID: domain.LithTaxiCoupon, Quantity: -1}}
		}
		return q
	}
	// A caller's fake beginner job cannot authorize a discount after the SQL lock.
	forged := sessions[0].Character
	forged.Job = 0
	sessions[0].Character = forged
	q := makeRequest(0, 1, 100, false)
	if _, err = s.ExecuteScriptExchange(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stale job accepted: %v", err)
	}
	sessions[0].Character, err = s.GetCharacter(ctx, sessions[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	// Regular1200 fare exceeds1000; neither fare nor saved map may change.
	q = makeRequest(0, 0, 1200, false)
	if _, err = s.ExecuteScriptExchange(ctx, q); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("insufficient %v", err)
	}
	unchanged, err := s.GetCharacter(ctx, sessions[0].CharacterID)
	if err != nil || unchanged.MapID != domain.LithTaxiMap || unchanged.Mesos != 1000 {
		t.Fatal(unchanged, err)
	}
	q = makeRequest(0, 1, 1000, false)
	wrongOwner := q
	wrongOwner.Session.AccountID = sessions[1].AccountID
	if _, err = s.ExecuteScriptExchange(ctx, wrongOwner); err == nil {
		t.Fatal("other account travelled actor")
	}
	stale := q
	stale.Session.Generation++
	if _, err = s.ExecuteScriptExchange(ctx, stale); err == nil {
		t.Fatal("stale owner travelled")
	}
	result, err := s.ExecuteScriptExchange(ctx, q)
	if err != nil || result.Character.MapID != 102000000 || result.Character.Mesos != 0 {
		t.Fatal(result, err)
	}
	replay, err := s.ExecuteScriptExchange(ctx, q)
	if err != nil || !replay.Replayed || replay.Character.Mesos != 0 || replay.Character.MapID != 102000000 {
		t.Fatal(replay, err)
	}
	modified := q
	tr := *q.Travel
	tc := *tr.Taxi
	tc.DisplayedFare = 100
	tr.Taxi = &tc
	modified.Travel = &tr
	if _, err = s.ExecuteScriptExchange(ctx, modified); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reused command fare changed: %v", err)
	}
	// Coupon consumes exactly one and persists exact Henesys portal6, without money.
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,4,1,$2,2)`, sessions[1].CharacterID, domain.LithTaxiCoupon); err != nil {
		t.Fatal(err)
	}
	sessions[1].Character, err = s.GetCharacter(ctx, sessions[1].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	q = makeRequest(1, 2, 0, true)
	bad := q
	bad.Items = []domain.ScriptItemDelta{{TemplateID: domain.LithTaxiCoupon, Quantity: -2}}
	if _, err = s.ExecuteScriptExchange(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("excess coupon %v", err)
	}
	result, err = s.ExecuteScriptExchange(ctx, q)
	if err != nil || result.Character.MapID != 100000000 || result.Character.SpawnPoint != 6 || result.Character.Mesos != 1000 || itemQuantity(result.Character, domain.LithTaxiCoupon) != 1 {
		t.Fatal(result, err)
	}
	replay, err = s.ExecuteScriptExchange(ctx, q)
	if err != nil || !replay.Replayed || itemQuantity(replay.Character, domain.LithTaxiCoupon) != 1 {
		t.Fatal(replay, err)
	}
	stored, err := s.GetCharacter(ctx, sessions[1].CharacterID)
	if err != nil || stored.MapID != 100000000 || stored.SpawnPoint != 6 || stored.Mesos != 1000 || itemQuantity(stored, domain.LithTaxiCoupon) != 1 {
		t.Fatal(stored, err)
	}
}

func TestTaxiConcurrentTripsCannotChargeTwice(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	actor := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET map_id=$1 WHERE id=$2`, domain.LithTaxiMap, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = s.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	m, err := catalog.Map(102000000)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := m.Spawn()
	if !ok {
		t.Fatal("no spawn")
	}
	q := domain.ScriptExchange{Session: actor, MesosDelta: -100, Travel: &domain.ScriptTravel{FromMap: domain.LithTaxiMap, MapID: m.ID, SpawnPoint: p.ID, X: p.X, Y: p.Y, Taxi: &domain.TaxiChoice{Choice: 1, DisplayedFare: 100}}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := q
			r.OperationID = fmt.Sprintf("taxi-concurrent-%d-%d", actor.CharacterID, i)
			_, e := s.ExecuteScriptExchange(ctx, r)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, domain.ErrStaleOwner) {
			t.Fatal(e)
		}
	}
	stored, e := s.GetCharacter(ctx, actor.CharacterID)
	if e != nil || success != 1 || stored.Mesos != 900 || stored.MapID != 102000000 {
		t.Fatal(success, stored, e)
	}
}
