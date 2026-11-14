package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

func (a *worldScript) departIsland(ctx context.Context, command scripting.Command) (scripting.CommandResult, error) {
	refuse := func(reason string) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": reason}}, nil
	}
	if a.npcID != 22000 || command.Script != "npc/22000.lua" {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	repo, ok := a.server.repo.(domain.ScriptRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	destination, err := a.server.gameplay.catalog.Map(2010000)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	entry, ok := destination.Spawn()
	if !ok {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	a.live.persistMu.Lock()
	a.live.mu.Lock()
	snapshot := a.live.session
	snapshot.Lease = a.server.currentLease()
	a.live.mu.Unlock()
	if snapshot.Character.MapID != 2000000 || snapshot.Character.HP <= 0 || !a.hasNPCContext(snapshot) || a.server.tradeActive(a.live) {
		a.live.persistMu.Unlock()
		return refuse("context_changed")
	}
	expectedVisit := a.server.sceneFor(a.live, a.sink)
	if expectedVisit == nil {
		a.live.persistMu.Unlock()
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	request := domain.ScriptExchange{Session: snapshot, OperationID: command.ID, Travel: &domain.ScriptTravel{FromMap: 2000000, MapID: 2010000, SpawnPoint: entry.ID, X: entry.X, Y: entry.Y}}
	letter := false
	for _, item := range snapshot.Character.Items {
		if item.TemplateID == 4031801 && item.Quantity > 0 {
			letter = true
			break
		}
	}
	if letter {
		request.Items = []domain.ScriptItemDelta{{TemplateID: 4031801, Quantity: -1}}
	} else if snapshot.Character.Level < 5 {
		a.live.persistMu.Unlock()
		return refuse("level")
	} else {
		request.MesosDelta = -150
	}
	commitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.ExecuteScriptExchange(commitCtx, request)
	cancel()
	if err != nil {
		a.live.persistMu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) {
			return refuse("fare")
		}
		return scripting.CommandResult{}, err
	}
	if result.Character.MapID != 2010000 {
		a.live.persistMu.Unlock()
		return refuse("already_travelled")
	}
	defer a.live.persistMu.Unlock()
	return a.publishScriptTravel(ctx, snapshot, result, expectedVisit)
}
