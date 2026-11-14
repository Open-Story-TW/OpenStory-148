package world

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

// The Java warp(map) chooses portal Randomizer.nextInt(portalCount), falling
// back to0 when that numeric ID is absent. Hash an authenticated command ID so
// the same durable operation always selects the same portal when retried.
func taxiPortal(m content.Map, operation string, coupon bool) (content.Portal, error) {
	wanted := 6
	if !coupon {
		if len(m.Portals) == 0 || len(m.Portals) > 256 || operation == "" {
			return content.Portal{}, domain.ErrInvalidInput
		}
		n := uint64(len(m.Portals))
		// Rejection sampling avoids modulo bias for non-power-of-two counts.
		threshold := -n % n
		wanted = -1
		for counter := uint64(0); counter < 8; counter++ {
			var suffix [8]byte
			binary.LittleEndian.PutUint64(suffix[:], counter)
			h := sha256.New()
			_, _ = h.Write([]byte("lith-taxi/" + operation))
			_, _ = h.Write(suffix[:])
			v := binary.LittleEndian.Uint64(h.Sum(nil)[:8])
			if v >= threshold {
				wanted = int(v % n)
				break
			}
		}
		if wanted < 0 {
			return content.Portal{}, domain.ErrInvalidInput
		}
	}
	for _, p := range m.Portals {
		if p.ID == wanted && p.ID >= 0 && p.ID <= 255 {
			return p, nil
		}
	}
	if !coupon {
		for _, p := range m.Portals {
			if p.ID == 0 {
				return p, nil
			}
		}
	}
	return content.Portal{}, domain.ErrInvalidInput
}

func (a *worldScript) takeTaxi(ctx context.Context, command scripting.Command) (scripting.CommandResult, error) {
	refuse := func(reason string) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": reason}}, nil
	}
	if a.npcID != domain.LithTaxiNPC || command.Script != "npc/1002007.lua" {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	if len(command.Ints) != 2 || command.Ints[0] < 0 || command.Ints[0] > 3 || command.Ints[1] < 0 || command.Ints[1] > 1200 || command.Text != "" || command.Exchange != nil {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	repo, ok := a.server.repo.(domain.ScriptRepository)
	if !ok || a.server.gameplay == nil {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	a.live.mu.Lock()
	snapshot := cloneCombatSession(a.live.session)
	snapshot.Lease = a.server.currentLease()
	a.live.mu.Unlock()
	if snapshot.Character.MapID != domain.LithTaxiMap || snapshot.Character.HP <= 0 || !a.hasNPCContext(snapshot) || a.server.tradeActive(a.live) || a.server.getEconomyDialog(a.live, "shop") != nil || a.server.getEconomyDialog(a.live, "storage") != nil {
		return refuse("context_changed")
	}
	plan, err := domain.LithTaxiPlan(snapshot.Character, int(command.Ints[0]), command.Ints[1])
	if err != nil {
		return refuse("fare_changed")
	}
	destination, err := a.server.gameplay.catalog.Map(plan.MapID)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	entry, err := taxiPortal(destination, command.ID, plan.UseCoupon)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	expectedVisit := a.server.sceneFor(a.live, a.sink)
	if expectedVisit == nil {
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	request := domain.ScriptExchange{Session: snapshot, OperationID: command.ID, MesosDelta: -plan.Fare,
		Travel: &domain.ScriptTravel{FromMap: domain.LithTaxiMap, MapID: plan.MapID, SpawnPoint: entry.ID, X: entry.X, Y: entry.Y,
			Taxi: &domain.TaxiChoice{Choice: int(command.Ints[0]), DisplayedFare: command.Ints[1]}}}
	if plan.UseCoupon {
		request.Items = []domain.ScriptItemDelta{{TemplateID: domain.LithTaxiCoupon, Quantity: -1}}
	}
	commitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.ExecuteScriptExchange(commitCtx, request)
	cancel()
	if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrStaleOwner) {
		return refuse("fare_or_context_changed")
	}
	if err != nil {
		return scripting.CommandResult{}, err
	}
	if result.Character.MapID != plan.MapID {
		return refuse("already_travelled")
	}
	return a.publishScriptTravel(ctx, snapshot, result, expectedVisit)
}
