package world

import (
	"context"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"time"
)

func (a *worldScript) advanceSecondJob(ctx context.Context, command scripting.Command) (scripting.CommandResult, error) {
	denied := scripting.CommandResult{Value: map[string]any{"ok": false}}
	if a.npcID != 1052001 || command.Script != "npc/1052001.lua" || len(command.Ints) != 1 || command.Ints[0] != 130 || command.Text != "" || command.Exchange != nil {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	repo, ok := a.server.repo.(domain.SecondJobAdvancementRepository)
	if !ok {
		return denied, nil
	}
	if a.server.tradeActive(a.live) || a.server.getEconomyDialog(a.live, "shop") != nil || a.server.getEconomyDialog(a.live, "storage") != nil {
		return denied, nil
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	snapshot := cloneCombatSession(a.server.skillSession(a.live))
	visit := a.server.sceneFor(a.live, a.sink)
	if visit == nil || !a.hasNPCContext(snapshot) {
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	if snapshot.Character.Job != 100 || snapshot.Character.Level < 30 {
		return denied, nil
	}
	request := domain.JobAdvancementRequest{Session: snapshot, OperationID: command.ID, NPCID: a.npcID, Script: command.Script, TargetJob: 130}
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.AdvanceSecondJob(call, request)
	cancel()
	if err != nil && !combatKnownFailure(err) {
		resolver, ok := a.server.repo.(domain.SecondJobOutcomeRepository)
		if !ok {
			return scripting.CommandResult{}, err
		}
		resolve, done := context.WithTimeout(ctx, 3*time.Second)
		var found bool
		result, found, err = resolver.ResolveSecondJob(resolve, request)
		done()
		if err == nil && !found {
			return denied, nil
		}
	}

	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrAlreadyExists) {
		return denied, nil
	}
	if err != nil {
		return scripting.CommandResult{}, err
	}
	unlock, err := a.server.lockScenePublication(snapshot.CharacterID)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	defer unlock()
	if !a.server.sceneCurrent(visit) {
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	if result.Character.ID != snapshot.CharacterID || result.Character.AccountID != snapshot.AccountID || result.Character.WorldID != snapshot.Lease.WorldID || result.Character.MapID != snapshot.Character.MapID || result.Character.Job != 130 || result.FromJob != 100 || result.ToJob != 130 || result.Character.Revision != snapshot.Character.Revision+1 {
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	// Publish one complete aggregate; do not expose an intermediate old-job
	// projection through applyInventoryResult before the job/SP fields merge.
	a.live.mu.Lock()
	if a.live.session.Character.Revision != snapshot.Character.Revision {
		a.live.mu.Unlock()
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	next := a.live.session
	current := result.Character
	current.X, current.Y, current.SpawnPoint = next.Character.X, next.Character.Y, next.Character.SpawnPoint
	next.Character = current
	if !a.server.updateOnlineOwned(snapshot, next) {
		a.live.mu.Unlock()
		return scripting.CommandResult{}, domain.ErrStaleOwner
	}
	a.live.session = next
	a.live.mu.Unlock()
	a.server.updateQuestDropSession(next)

	if err = a.sink.WritePacket(protocol.JobAdvancementStats(current)); err != nil {
		return scripting.CommandResult{}, err
	}
	a.server.refreshCharacterLookScene(visit)
	a.server.refreshParty(a.live)
	return scripting.CommandResult{Value: map[string]any{"ok": true}}, nil
}
