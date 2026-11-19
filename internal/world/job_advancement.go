package world

import (
	"context"
	"errors"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func (a *worldScript) advanceWarrior(ctx context.Context, command scripting.Command) (scripting.CommandResult, error) {
	repo, ok := a.server.repo.(domain.JobAdvancementRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	return a.commitFirstJob(ctx, command, 100, repo.AdvanceWarrior)
}

func (a *worldScript) advanceFirstJob(ctx context.Context, command scripting.Command, targetJob int) (scripting.CommandResult, error) {
	repo, ok := a.server.repo.(domain.FirstJobAdvancementRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	return a.commitFirstJob(ctx, command, targetJob, repo.AdvanceFirstJob)
}

func (a *worldScript) commitFirstJob(ctx context.Context, command scripting.Command, targetJob int, advance func(context.Context, domain.JobAdvancementRequest) (domain.JobAdvancementResult, error)) (scripting.CommandResult, error) {
	denied := scripting.CommandResult{Value: map[string]any{"ok": false}}
	if a.server.tradeActive(a.live) || a.server.getEconomyDialog(a.live, "shop") != nil || a.server.getEconomyDialog(a.live, "storage") != nil {
		return denied, nil
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	a.live.mu.Lock()
	snapshot := a.live.session
	snapshot.Lease = a.server.currentLease()
	a.live.mu.Unlock()
	if !a.hasNPCContext(snapshot) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	result, err := advance(ctx, domain.JobAdvancementRequest{Session: snapshot, OperationID: command.ID, NPCID: a.npcID, Script: command.Script, TargetJob: targetJob})
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrAlreadyExists) {
		return denied, nil
	}
	if err != nil {
		return scripting.CommandResult{}, err
	}
	a.server.applyProgressResult(a.live, result.Character)
	a.live.mu.Lock()
	if a.live.session.Character.Revision == result.Character.Revision {
		// Quest-bound advancements also teach beginner skills and move SP
		// between books; applyProgressResult leaves both to the caller.
		a.live.session.Character.Skills = result.Character.Skills
		a.live.session.Character.SPBooks = result.Character.SPBooks
	}
	current := a.live.session.Character
	a.live.mu.Unlock()
	// The native inventory arrays must grow before a newly granted slot is
	// decoded. All of these effects follow one committed advancement receipt.
	for _, invType := range []int{1, 2, 3, 4} {
		if current.Capacity(invType) == snapshot.Character.Capacity(invType) {
			continue
		}
		if err = a.sink.WritePacket(protocol.InventoryCapacityUpdate(invType, current.Capacity(invType))); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	if len(result.Changes) != 0 {
		if err = a.sink.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	if err = a.sink.WritePacket(protocol.JobAdvancementStats(current)); err != nil {
		return scripting.CommandResult{}, err
	}
	if current.Level != snapshot.Character.Level {
		// An advancement that also raised the level (the Phantom tutorial).
		if err = a.sink.WritePacket(protocol.UpdateCombatStats(current)); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	for _, skill := range current.Skills {
		for _, before := range snapshot.Character.Skills {
			if before.ID == skill.ID && before.Level == skill.Level {
				skill.ID = 0
			}
		}
		if skill.ID != 0 {
			if err = a.sink.WritePacket(protocol.SkillUpdate(skill)); err != nil {
				return scripting.CommandResult{}, err
			}
		}
	}
	for _, quest := range result.Quests {
		if err = a.sink.WritePacket(protocol.QuestUpdate(quest)); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	a.server.refreshCharacterLook(a.live)
	if current.Job != snapshot.Character.Job {
		a.server.respawnAfterJobChange(a.live)
	}
	return scripting.CommandResult{Value: map[string]any{"ok": true}}, nil
}
