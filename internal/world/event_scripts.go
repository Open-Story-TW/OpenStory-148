package world

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

var eventWord = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// eventEffects are the party quest results a stage NPC shows the whole map.
var eventEffects = map[string][2]string{
	"clear": {"quest/party/clear", "Party1/Clear"},
	"wrong": {"quest/party/wrong_kor", "Party1/Failed"},
}

// eventCommand serves the npc event_* calls. Everything except event_status
// and event_start acts on the group the character belongs to.
func (a *worldScript) eventCommand(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	s := a.server
	result := func(value any) (scripting.CommandResult, error) { return scripting.CommandResult{Value: value}, nil }
	invalid := scripting.CommandResult{}
	if len(command.Ints) != 0 && command.Kind != "event_party_exp" {
		return invalid, domain.ErrInvalidInput
	}
	switch command.Kind {
	case "event_status":
		if !eventWord.MatchString(command.Text) {
			return invalid, domain.ErrInvalidInput
		}
		value := s.eventStatus(snapshot.ChannelID, command.Text)
		inst := s.eventOf(snapshot.CharacterID)
		value["inside"] = inst != nil && inst.definition.Name == command.Text
		if value["inside"] == true {
			value["attempt"] = inst.id
		}
		return result(value)
	case "event_start":
		if !eventWord.MatchString(command.Text) {
			return invalid, domain.ErrInvalidInput
		}
		reason, here, qualified, err := s.eventStart(ctx, command.Text, snapshot.ChannelID, a.live, a.sink)
		return scripting.CommandResult{Value: map[string]any{"ok": reason == "" && err == nil, "reason": reason, "here": int64(here), "qualified": int64(qualified)}}, err
	case "event_get":
		if !eventWord.MatchString(command.Text) {
			return invalid, domain.ErrInvalidInput
		}
		return result(s.eventProperty(snapshot.CharacterID, command.Text))
	case "event_set":
		key, value, ok := strings.Cut(command.Text, "=")
		if !ok || !eventWord.MatchString(key) || strings.ContainsRune(value, 0) {
			return invalid, domain.ErrInvalidInput
		}
		return invalid, s.setEventProperty(snapshot.CharacterID, key, value)
	case "event_effect":
		effect, ok := eventEffects[command.Text]
		if !ok || s.eventOf(snapshot.CharacterID) == nil {
			return invalid, domain.ErrInvalidInput
		}
		s.broadcastInventoryPacket(snapshot.ChannelID, snapshot.Character.MapID, protocol.FieldScreenEffect(effect[0]))
		s.broadcastInventoryPacket(snapshot.ChannelID, snapshot.Character.MapID, protocol.FieldSoundEffect(effect[1]))
		if command.Text == "clear" {
			s.broadcastInventoryPacket(snapshot.ChannelID, snapshot.Character.MapID, protocol.FieldObjectEffect("gate"))
		}
		return invalid, nil
	}
	inst := s.eventOf(snapshot.CharacterID)
	if command.Text != "" || inst == nil {
		return invalid, domain.ErrForbidden
	}
	switch command.Kind {
	case "event_summon":
		if inst.definition.Name == "PinkBean" {
			started, err := a.summonPinkBean(ctx, inst, snapshot, command)
			return scripting.CommandResult{Value: started}, err
		}
		return result(s.summonExpeditionBoss(inst, snapshot, a.npcID, a.objectID))
	case "event_areas":
		mask, err := s.eventAreaMask(ctx, inst, snapshot)
		return scripting.CommandResult{Value: mask}, err
	case "event_party_exp":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 1000000 {
			return invalid, domain.ErrInvalidInput
		}
		if s.partySnapshotOf(snapshot.CharacterID).LeaderID != snapshot.CharacterID {
			return invalid, domain.ErrForbidden
		}
		return invalid, s.awardEventStageEXP(ctx, inst, snapshot.Character.MapID, command.Ints[0])
	case "event_finish":
		if s.partySnapshotOf(snapshot.CharacterID).LeaderID != snapshot.CharacterID {
			return invalid, domain.ErrForbidden
		}
		s.eventEnd(inst)
	case "event_leave":
		go s.eventExit(inst.channel, inst.definition, a.live, a.sink)
	default:
		return invalid, domain.ErrInvalidInput
	}
	return invalid, nil
}

func (s *Server) eventAreaMask(ctx context.Context, inst *eventInstance, actor domain.CharacterSession) (int64, error) {
	mapID := actor.Character.MapID
	field, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return 0, err
	}
	var mask int64
	mark := func(x, y int) {
		for i, area := range field.Areas {
			if i < 63 && area.Contains(x, y) {
				mask |= 1 << i
			}
		}
	}
	for _, occupant := range s.eventOccupants(inst.channel, []int{mapID}) {
		peer := partySession(occupant.live)
		c := peer.Character
		if c.MapID == mapID && c.HP > 0 && s.eventOf(peer.CharacterID) == inst {
			mark(c.X, c.Y)
		}
	}
	if repo, ok := s.repo.(domain.InventoryRepository); ok {
		drops, err := repo.ListItemDrops(ctx, s.cfg.WorldID, inst.channel, mapID)
		if err != nil {
			return 0, err
		}
		for _, drop := range drops {
			if drop.PlayerDrop && s.eventOf(drop.OwnerID) == inst && (mapID != content.LudiPQQuiz || drop.TemplateID == content.LudiPQPuppet) {
				mark(drop.X, drop.Y)
			}
		}
	}
	return mask, nil
}

func (s *Server) awardEventStageEXP(ctx context.Context, inst *eventInstance, mapID int, exp int64) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return domain.ErrForbidden
	}
	id := fmt.Sprintf("event-%s-exp-%d", inst.id, mapID)
	for _, occupant := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		err := func() error {
			occupant.live.persistMu.Lock()
			defer occupant.live.persistMu.Unlock()
			session := s.skillSession(occupant.live)
			visit := s.sceneFor(occupant.live, occupant.sink)
			if visit == nil || s.eventOf(session.CharacterID) != inst || !inst.definition.Stage(session.Character.MapID) {
				return nil
			}
			result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: session, OperationID: id, EXP: exp})
			if err != nil {
				return err
			}
			s.applyProgressResult(occupant.live, result.Character)
			if !result.Replayed {
				return s.writeScenePackets(visit, protocol.UpdateCombatStats(result.Character), protocol.ShowExperienceGain(exp, true))
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// The v148 Kerning maps share the party_portal script for their four stage
// transitions. The server checks progress before resolving the next entrance.
func (s *Server) kerningStagePortal(ctx context.Context, snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	mapID := snapshot.Character.MapID
	if portal.Script != "party_portal" || mapID < 910340100 || mapID > 910340400 || mapID%100 != 0 {
		return false, nil
	}
	stage := (mapID - 910340000) / 100
	inst := s.eventOf(snapshot.CharacterID)
	if inst == nil || inst.definition.Name != "KerningPQ" || s.eventProperty(snapshot.CharacterID, strconv.Itoa(stage)+"stageclear") != "true" {
		_ = sink.WritePacket(protocol.PortalTrainingMessage("請先完成這一關。"))
		return true, sink.WritePacket(protocol.EnableActions())
	}
	next, err := s.gameplay.catalog.Map(mapID + 100)
	if err != nil {
		return true, err
	}
	entry, ok := next.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	return true, s.warpEventMember(ctx, inst, mapID, eventOccupant{live, sink}, next.ID, entry)
}
