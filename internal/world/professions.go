package world

import (
	"context"
	"errors"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// commitProfession runs one profession exchange (items plus a profession
// change) for a live character and publishes the outcome: inventory, the
// profession records the client keeps as skills, traits and fatigue. ok is
// false when a rule or the inventory refused it; nothing changed then.
func (s *Server) commitProfession(ctx context.Context, live *liveCharacter, sink packetSink, operationID string, items []domain.ScriptItemDelta, change domain.ProfessionChange) (after domain.Character, ok bool, err error) {
	repo, has := s.repo.(domain.ScriptRepository)
	if !has {
		return after, false, domain.ErrForbidden
	}
	if s.tradeActive(live) {
		return after, false, nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	snapshot := live.session
	snapshot.Lease = s.currentLease()
	live.mu.Unlock()
	before := snapshot.Character
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: snapshot, OperationID: operationID, Items: items, Profession: &change})
	if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrAlreadyExists) {
		return before, false, nil
	}
	if err != nil {
		return before, false, err
	}
	after = result.Character
	s.applyInventoryResult(live, after)
	live.mu.Lock()
	if c := &live.session.Character; c.Revision == after.Revision {
		// The store wore the fatigue off up to this commit.
		before.Fatigue = c.Fatigue
		c.Professions, c.Recipes, c.Traits, c.Fatigue = after.Professions, after.Recipes, after.Traits, after.Fatigue
		live.fatigueAt = time.Now()
	}
	live.mu.Unlock()
	if result.Replayed {
		return after, true, nil
	}
	if len(result.Changes) > 0 {
		if err = sink.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
			return after, true, err
		}
	}
	for _, packet := range protocol.ProfessionChanges(before, after) {
		if err = sink.WritePacket(packet); err != nil {
			return after, true, err
		}
	}
	return after, true, nil
}

// professionScriptCommand serves the profession host commands of NPC scripts.
func (a *worldScript) professionScriptCommand(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	result := func(ok bool) (scripting.CommandResult, error) {
		if !ok {
			return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": "conditions_not_met"}}, nil
		}
		return scripting.CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	if command.Kind == "open_crafting" {
		if len(command.Ints) != 0 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return scripting.CommandResult{}, a.sink.WritePacket(protocol.OpenCraftingWindow())
	}
	if len(command.Ints) != 1 || !content.ValidProfession(int(command.Ints[0])) {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	id := int(command.Ints[0])
	switch command.Kind {
	case "profession_level":
		return scripting.CommandResult{Value: int64(snapshot.Character.Profession(id).Level)}, nil
	case "profession_forget":
		_, ok, err := a.server.commitProfession(ctx, a.live, a.sink, command.ID, nil, domain.ProfessionChange{Forget: id})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return result(ok)
	case "profession_learn":
		// A gathering master hands out the first tool when there is room for
		// it; a full equipment tab does not stop the lesson.
		if tool := content.StarterGatherTool(id); tool != 0 && canHoldItem(snapshot.Character, tool, 1) {
			_, ok, err := a.server.commitProfession(ctx, a.live, a.sink, command.ID, []domain.ScriptItemDelta{{TemplateID: tool, Quantity: 1}}, domain.ProfessionChange{Learn: id})
			if err != nil {
				return scripting.CommandResult{}, err
			}
			return result(ok)
		}
		_, ok, err := a.server.commitProfession(ctx, a.live, a.sink, command.ID, nil, domain.ProfessionChange{Learn: id})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return result(ok)
	}
	return scripting.CommandResult{}, domain.ErrInvalidInput
}

// wzRoot is where the profession definitions are read from; empty means the
// server runs on embedded content only and has no professions.
func wzRoot() string { return os.Getenv("MAPLEGO_WZ_ROOT") }
