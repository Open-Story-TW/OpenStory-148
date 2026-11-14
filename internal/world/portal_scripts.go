package world

import (
	"context"
	"fmt"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

type portalScriptHost struct {
	server   *Server
	live     *liveCharacter
	sink     packetSink
	script   *worldScript
	identity scripting.Identity
	channel  int
	mapID    int
	portal   content.Portal
	// openNPC is the NPC a generated Legacy source asked to talk to
	// (pi.openNpc); it opens after the portal invocation has ended.
	openNPC int
}

// Portal invocation is separate from a suspended NPC conversation. Reviewed
// sources may warp and run narrowly scoped training queries; generated Legacy
// sources (scripts/legacy/portal_driver.lua) may also read the character,
// commit one exchange and open an NPC conversation. The host never keeps an
// invisible dialogue suspended, and every command revalidates the source
// portal.
func (s *Server) runPortalScript(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) error {
	live.mu.Lock()
	snapshot := live.session
	live.mu.Unlock()
	mapID := snapshot.Character.MapID
	name := "portal/" + p.Script + ".lua"
	if s.scripts == nil || p.Script == "" {
		return sink.WritePacket(protocol.EnableActions())
	}
	if strings.ContainsAny(p.Script, "/\\.") {
		s.portalGMNotice(live, sink, mapID, p, "未處理(腳本名稱不合法)")
		return sink.WritePacket(protocol.EnableActions())
	}
	if scene := sceneScriptName(portalSceneScripts, p.Script, mapID); scene != "" {
		return s.startScene(ctx, channel, live, sink, scene, "傳點 "+p.Name)
	}
	if _, err := scripts.Source(name); err != nil {
		if reason, ok := scripts.LegacyPortalPendingReason(p.Script); ok {
			s.portalGMNotice(live, sink, mapID, p, "未處理(Legacy portal/%s.js 尚未移植: %s)", p.Script, reason)
		} else {
			s.portalGMNotice(live, sink, mapID, p, "未處理(伺服器與 Legacy 都沒有這個傳點腳本)")
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	id := scripting.Identity{SessionID: snapshot.SessionID, CharacterID: snapshot.CharacterID, Generation: snapshot.Generation}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	a := &worldScript{server: s, live: live, sink: sink, kind: "portal", identity: id, portal: p, mapID: mapID}
	host := &portalScriptHost{server: s, live: live, sink: sink, script: a, identity: id, channel: channel, mapID: mapID, portal: p}
	s.scripts.mu.Lock()
	var options scripting.StartOptions
	if arm, ok := s.scripts.armed[snapshot.CharacterID]; ok && arm.identity == id {
		options = arm.options
	}
	delete(s.scripts.armed, snapshot.CharacterID)
	s.scripts.active[snapshot.CharacterID] = a
	s.scripts.mu.Unlock()
	options.Host, options.API = host, "portal"
	done, err := func() (bool, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.gmNotice("%s 開始", name)
		session, event, err := s.scripts.engine.Start(ctx, id, name, options)
		a.session = session
		if err != nil {
			return false, a.fail(err)
		}
		return event.Kind == "done", a.deliver(event)
	}()
	if err != nil || !done || host.openNPC == 0 {
		return err
	}
	return s.openPortalNPC(ctx, channel, live, sink, host.openNPC)
}

// openPortalNPC starts npc/<id>.lua for Legacy pi.openNpc. There is no NPC
// object to stand next to, so the conversation is bound to the character's
// current map instead.
func (s *Server) openPortalNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, npcID int) error {
	return s.openChainedNPC(ctx, channel, live, sink, npcID, 0)
}

// openChainedNPC is openPortalNPC for a conversation that another
// conversation opened (Legacy cm.openNpc); chain is how many came before it.
func (s *Server) openChainedNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, npcID, chain int) error {
	name := fmt.Sprintf("npc/%d.lua", npcID)
	if _, err := scripts.Source(name); err != nil {
		s.npcGMNotice(live, sink, npcID, "未處理(要開啟的 %s 腳本不存在)", name)
		return sink.WritePacket(protocol.EnableActions())
	}
	return s.startRemoteScript(ctx, channel, live, sink, npcID, name, "由腳本開啟", chain)
}

// startRemoteScript runs a script whose NPC is not on the character's map.
// The conversation is bound to the map the character stands on instead.
func (s *Server) startRemoteScript(ctx context.Context, channel int, live *liveCharacter, sink packetSink, npcID int, name, origin string, chain int) error {
	live.mu.Lock()
	snapshot := live.session
	live.mu.Unlock()
	if s.scripts == nil || snapshot.ChannelID != channel || snapshot.Character.HP <= 0 || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	s.cancelNPCScriptState(live, false)
	a := &worldScript{server: s, live: live, sink: sink, kind: "npc", npcID: npcID, remoteMap: snapshot.Character.MapID, chain: chain,
		identity: scripting.Identity{SessionID: snapshot.SessionID, CharacterID: snapshot.CharacterID, Generation: snapshot.Generation}}
	s.scripts.mu.Lock()
	s.scripts.active[snapshot.CharacterID] = a
	s.scripts.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gmNotice("%s 開始 (%s)", name, origin)
	session, event, err := s.scripts.engine.Start(ctx, a.identity, name, scripting.StartOptions{Host: a})
	a.session = session
	if err != nil {
		return a.fail(err)
	}
	return a.deliver(event)
}

func (h *portalScriptHost) Execute(ctx context.Context, id scripting.Identity, command scripting.Command) (scripting.CommandResult, error) {
	result, err := h.execute(ctx, id, command)
	if h.script != nil {
		h.script.gmCommand(command, result, err)
	}
	return result, err
}

// legacyPortalQueries are the character reads a generated Legacy portal
// source may make; worldScript answers them as it does for an NPC.
var legacyPortalQueries = map[string]bool{"map_id": true, "level": true, "job": true, "hp": true, "gender": true, "mesos": true,
	"item_count": true, "can_hold": true, "quest_state": true, "quest_data": true, "exchange": true}

func (h *portalScriptHost) execute(ctx context.Context, id scripting.Identity, command scripting.Command) (scripting.CommandResult, error) {
	h.live.mu.Lock()
	snapshot := h.live.session
	h.live.mu.Unlock()
	if id != h.identity || snapshot.SessionID != id.SessionID || snapshot.Generation != id.Generation || snapshot.CharacterID != id.CharacterID {
		return scripting.CommandResult{}, scripting.ErrIdentity
	}
	if snapshot.ChannelID != h.channel || snapshot.Character.MapID != h.mapID || snapshot.Character.HP <= 0 ||
		!within(snapshot.Character.X, snapshot.Character.Y, h.portal.X, h.portal.Y, 150) || h.server.tradeActive(h.live) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	if h.portal.Script == "entertraining" {
		return h.executeTraining(ctx, snapshot, command)
	}
	// Ardentmill's ticket portal and its Free Market door are written by hand
	// and use the same reads, notice and atomic exchange as a generated
	// Legacy source.
	if scripts.LegacyPortal(command.Script) || command.Script == "portal/gatherTuto.lua" || command.Script == "portal/market300.lua" || command.Script == "portal/PTtutor100_0.lua" {
		if result, handled, err := h.executeLegacy(ctx, id, snapshot, command); handled {
			return result, err
		}
	}
	if command.Kind != "warp" {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	destination, entry, err := scriptDestination(h.server.gameplay.catalog, command)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	return scripting.CommandResult{}, h.server.warpGameplay(ctx, h.channel, h.live, h.sink, destination.ID, entry)
}

// executeLegacy answers the commands only generated Legacy portal sources use.
// handled is false for a warp, which takes the common path.
func (h *portalScriptHost) executeLegacy(ctx context.Context, id scripting.Identity, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, bool, error) {
	value := func(v any) (scripting.CommandResult, bool, error) {
		return scripting.CommandResult{Value: v}, true, nil
	}
	switch command.Kind {
	case "warp":
		return scripting.CommandResult{}, false, nil
	case "portal_message":
		if len(command.Ints) > 1 || command.Text == "" || len(command.Text) > 512 {
			return scripting.CommandResult{}, true, domain.ErrInvalidInput
		}
		// Every Legacy kind is shown as the type-5 chat notice, the only
		// broadcast layout confirmed on the native client so far.
		return scripting.CommandResult{}, true, h.sink.WritePacket(protocol.PortalTrainingMessage(command.Text))
	case "portal_name":
		return value(h.portal.Name)
	case "portal_id":
		return value(int64(h.portal.ID))
	case "player_count":
		if len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] > 999999999 {
			return scripting.CommandResult{}, true, domain.ErrInvalidInput
		}
		return value(int64(h.server.mapPlayerCount(h.channel, int(command.Ints[0]))))
	case "monster_count":
		return value(int64(h.server.mapMonsterCount(h.channel, h.mapID)))
	case "fame":
		return value(int64(snapshot.Character.Fame))
	case "gm":
		return value(int64(snapshot.Character.GM))
	case "party_size", "party_leader":
		party := h.server.partySnapshotOf(snapshot.CharacterID)
		if command.Kind == "party_leader" {
			return value(party.ID != 0 && party.LeaderID == snapshot.CharacterID)
		}
		return value(int64(len(party.Members)))
	case "warp_party":
		return scripting.CommandResult{}, true, h.warpParty(ctx, snapshot, command)
	case "open_npc":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 9999999 {
			return scripting.CommandResult{}, true, domain.ErrInvalidInput
		}
		h.openNPC = int(command.Ints[0])
		return scripting.CommandResult{}, true, nil
	}
	if legacyPortalQueries[command.Kind] && h.script != nil {
		result, err := h.script.executeCommand(ctx, id, command)
		return result, true, err
	}
	return scripting.CommandResult{}, true, fmt.Errorf("%w: portal command %q", domain.ErrForbidden, command.Kind)
}

// partySnapshotOf is the character's current party; ID 0 means none.
func (s *Server) partySnapshotOf(id int64) domain.PartySnapshot {
	if s.parties == nil {
		return domain.PartySnapshot{}
	}
	s.parties.mu.Lock()
	defer s.parties.mu.Unlock()
	if p := s.parties.peers[id]; p != nil {
		return p.party
	}
	return domain.PartySnapshot{}
}

// warpParty is Legacy warpParty(map, portal): without a party, or alone in
// one, only the character moves. Otherwise every member who is alive, on
// this channel and on the character's map moves, the character last so the
// portal invocation stays valid until the others have left.
func (h *portalScriptHost) warpParty(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) error {
	if len(command.Ints) != 2 {
		return domain.ErrInvalidInput
	}
	destination, entry, err := scriptDestination(h.server.gameplay.catalog, scripting.Command{Kind: "warp", Ints: command.Ints})
	if err != nil {
		return err
	}
	type member struct {
		live *liveCharacter
		sink packetSink
	}
	var others []member
	if s := h.server; s.parties != nil {
		s.parties.mu.Lock()
		if me := s.parties.peers[snapshot.CharacterID]; me != nil && me.party.ID != 0 {
			for _, m := range me.party.Members {
				if peer := s.parties.peers[m.CharacterID]; peer != nil && m.CharacterID != snapshot.CharacterID && peer.party.ID == me.party.ID {
					others = append(others, member{peer.live, peer.sink})
				}
			}
		}
		s.parties.mu.Unlock()
	}
	for _, m := range others {
		m.live.mu.Lock()
		there := m.live.session
		m.live.mu.Unlock()
		if there.ChannelID != h.channel || there.Character.MapID != h.mapID || there.Character.HP <= 0 || h.server.tradeActive(m.live) {
			continue
		}
		h.server.cancelNPCScriptState(m.live, false)
		h.server.closeEconomyDialog(m.live)
		// A member who cannot be moved (for example one changing maps right
		// now) stays behind, as a Legacy member on another map did.
		_ = h.server.warpGameplay(ctx, h.channel, m.live, m.sink, destination.ID, entry)
	}
	return h.server.warpGameplay(ctx, h.channel, h.live, h.sink, destination.ID, entry)
}

func (s *Server) mapPlayerCount(channel, mapID int) int {
	if s.gameplay == nil {
		return 0
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	if room := s.gameplay.rooms[mapKey{channel: channel, mapID: mapID}]; room != nil {
		return len(room.members)
	}
	return 0
}

func (s *Server) mapMonsterCount(channel, mapID int) int {
	if s.gameplay == nil {
		return 0
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	count := 0
	if room := s.gameplay.rooms[mapKey{channel: channel, mapID: mapID}]; room != nil {
		for _, mob := range room.mobs {
			if mob.hp > 0 {
				count++
			}
		}
	}
	return count
}

func (h *portalScriptHost) executeTraining(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	if err := h.validateTrainingPortal(command); err != nil {
		return scripting.CommandResult{}, err
	}
	if command.Kind == "quest_state" && (len(command.Ints) != 1 || command.Ints[0] < 1041 || command.Ints[0] > 1044 || command.Text != "") {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	repo, ok := h.server.repo.(domain.QuestRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	snapshot.Lease = h.server.currentLease()
	states, err := repo.QuestStates(ctx, snapshot)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	active := 0
	for q := 1041; q <= 1044 && active == 0; q++ {
		for _, state := range states {
			if state.ID == q && state.State == 1 {
				active = q
				break
			}
		}
	}
	switch command.Kind {
	case "quest_state":
		for _, state := range states {
			if state.ID == int(command.Ints[0]) {
				return scripting.CommandResult{Value: int64(state.State)}, nil
			}
		}
		return scripting.CommandResult{Value: int64(0)}, nil
	case "portal_message":
		if active != 0 || len(command.Ints) != 0 || command.Text != trainingPortalDenied {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		return scripting.CommandResult{}, h.sink.WritePacket(protocol.PortalTrainingMessage(command.Text))
	case "warp":
		if active == 0 || len(command.Ints) != 2 || command.Ints[0] != int64(1010100+(active-1041)*100) || command.Ints[1] != 4 || command.Text != "" {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
	default:
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	destination, entry, err := scriptDestination(h.server.gameplay.catalog, command)
	if err != nil {
		return scripting.CommandResult{}, err
	}
	return scripting.CommandResult{}, h.server.warpGameplay(ctx, h.channel, h.live, h.sink, destination.ID, entry)
}

const trainingPortalDenied = "Only the adventurers that have been trained by Mai may enter."

func (h *portalScriptHost) validateTrainingPortal(command scripting.Command) error {
	if h.portal.Script != "entertraining" || h.mapID != 1010000 || command.Script != "portal/entertraining.lua" {
		return domain.ErrForbidden
	}
	m, err := h.server.gameplay.catalog.Map(h.mapID)
	if err != nil {
		return err
	}
	for _, p := range m.Portals {
		if p == h.portal && p.Name == "in00" && p.Type == 7 {
			return nil
		}
	}
	return domain.ErrForbidden
}
