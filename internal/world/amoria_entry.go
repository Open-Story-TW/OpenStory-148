package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

var amoriaTicketLog = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "amoria-ticket-at"}

func (a *worldScript) amoriaCommand(ctx context.Context, snapshot domain.CharacterSession, cmd scripting.Command) (scripting.CommandResult, error) {
	result := func(value any) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: value}, nil
	}
	if !a.hasNPCContext(snapshot) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	if cmd.Kind == "amoria_stage" {
		nativeBonus := a.npcID == content.AmoriaBonusNPC && cmd.Script == "npc/amoria_bonus.lua" &&
			snapshot.Character.MapID == content.AmoriaBonus
		if !nativeBonus && cmd.Script != fmt.Sprintf("npc/%d.lua", a.npcID) {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		status, err := a.server.amoriaStage(ctx, snapshot, eventOccupant{a.live, a.sink}, a.npcID)
		return scripting.CommandResult{Value: status}, err
	}
	if a.npcID != content.AmoriaGatekeeper || cmd.Script != "npc/9201043.lua" ||
		snapshot.Character.MapID != content.AmoriaApproach {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	if snapshot.Character.Level < 40 {
		return result("level")
	}
	repo, ok := a.server.repo.(domain.MarriageRepository)
	if !ok {
		return result("marriage")
	}
	married, err := repo.CharacterMarried(ctx, snapshot)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	if !married {
		return result("marriage")
	}
	switch cmd.Kind {
	case "amoria_entry":
		if henesysItemCount(snapshot.Character, content.AmoriaTicket) != 0 {
			return result("ticket")
		}
		if henesysItemCount(snapshot.Character, content.AmoriaKey) >= 10 {
			return result("keys")
		}
		return result("items")
	case "amoria_ticket":
		return a.issueAmoriaTicket(ctx, snapshot, cmd.ID)
	case "amoria_board":
		return a.takeTransitFare(ctx, cmd.ID, content.AmoriaLobby, content.AmoriaTicket, 0,
			func(current domain.CharacterSession) bool {
				return current.Character.MapID == content.AmoriaApproach && current.Character.HP > 0 &&
					sameSceneOwner(snapshot, current)
			})
	}
	return scripting.CommandResult{}, domain.ErrInvalidInput
}

func (a *worldScript) issueAmoriaTicket(ctx context.Context, snapshot domain.CharacterSession, op string) (scripting.CommandResult, error) {
	invalid := scripting.CommandResult{Value: "items"}
	s := a.server
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return invalid, domain.ErrForbidden
	}
	now := time.Now()
	logs, err := repo.ReadScriptLogs(ctx, snapshot, []domain.ScriptLogKey{amoriaTicketLog})
	if err != nil {
		return invalid, err
	}
	var last int64
	if len(logs) != 0 {
		last = logs[0].Value
	}
	if now.Before(time.Unix(last, 0).Add(6 * time.Hour)) {
		return scripting.CommandResult{Value: "cooldown"}, nil
	}
	issuedAt := now.Unix()
	err = s.applyEventExchange(ctx, eventOccupant{a.live, a.sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != content.AmoriaApproach || !sameSceneOwner(snapshot, current) ||
			!a.hasNPCContext(current) || s.tradeActive(a.live) || henesysItemCount(current.Character, content.AmoriaTicket) != 0 {
			return domain.ScriptExchange{}, domain.ErrForbidden
		}
		return domain.ScriptExchange{
			Items: []domain.ScriptItemDelta{
				{TemplateID: content.AmoriaKey, Quantity: -10},
				{TemplateID: content.AmoriaTicket, Quantity: 1},
			},
			Logs: []domain.ScriptLogChange{{Key: amoriaTicketLog, Expected: &last, Set: &issuedAt}},
		}, nil
	})
	if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrForbidden) ||
		errors.Is(err, domain.ErrAlreadyExists) {
		return invalid, nil
	}
	if err != nil {
		return invalid, err
	}
	return scripting.CommandResult{Value: "issued"}, nil
}

func (s *Server) prepareAmoria(ctx context.Context, inst *eventInstance, group []eventOccupant) (string, error) {
	marriages, ok := s.repo.(domain.MarriageRepository)
	if !ok {
		return "marriage", nil
	}
	var genders [2]bool
	for _, member := range group {
		peer := partySession(member.live)
		if peer.Character.MapID != content.AmoriaLobby || peer.Character.Gender < 0 || peer.Character.Gender > 1 {
			return "marriage", nil
		}
		married, err := marriages.CharacterMarried(ctx, peer)
		if err != nil {
			return "unavailable", err
		}
		if !married {
			return "marriage", nil
		}
		genders[peer.Character.Gender] = true
	}
	if !genders[0] || !genders[1] {
		return "marriage", nil
	}
	drops, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return "unavailable", domain.ErrForbidden
	}
	for _, id := range inst.definition.Maps {
		if err := drops.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, id); err != nil {
			return "unavailable", err
		}
	}
	for _, member := range group {
		if err := s.applyEventExchange(ctx, member, "amoria-start-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
			if current.Character.MapID != content.AmoriaLobby || s.eventOf(current.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			var result domain.ScriptExchange
			for _, id := range [...]int{content.AmoriaShard, content.AmoriaFang, content.AmoriaHammer, content.AmoriaCode} {
				if n := henesysItemCount(current.Character, id); n > 0 {
					result.Items = append(result.Items, domain.ScriptItemDelta{TemplateID: id, Quantity: -n})
				}
			}
			return result, nil
		}); err != nil {
			return "unavailable", err
		}
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{inst.channel, content.AmoriaMirror}]
	if room == nil || len(room.mobs) == 0 || len(room.reactors) != 1 {
		return "unavailable", domain.ErrNotFound
	}
	for _, mob := range room.mobs {
		mob.oneTime = true
	}
	return "", nil
}
