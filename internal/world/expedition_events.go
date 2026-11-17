package world

import (
	"context"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The party command lock serializes this roster with native expedition
// invites and party moves. The peers snapshot does not require any DB reads.
func (s *Server) eventExpeditionRoster(e content.Event, leader int64, live *liveCharacter) (int64, int, []eventOccupant, string) {
	b := s.parties
	b.mu.Lock()
	defer b.mu.Unlock()
	as := b.peers[leader]
	if as == nil || as.live != live {
		return 0, 0, nil, "unavailable"
	}
	exp := b.exp.byParty[as.party.ID]
	if exp == nil {
		return 0, 0, nil, "no_expedition"
	}
	if exp.kind != e.ExpeditionType {
		return 0, 0, nil, "expedition_type"
	}
	if exp.leader != leader {
		return 0, 0, nil, "not_leader"
	}
	var group []eventOccupant
	for _, pid := range exp.parties {
		for _, m := range exp.snapshots[pid].Members {
			if p := b.peers[m.CharacterID]; p != nil && p.party.ID == pid && m.CharacterID != leader {
				group = append(group, eventOccupant{p.live, p.sink})
			}
		}
	}
	return exp.id, exp.memberCount(), group, ""
}

// Revoke admission immediately when a member is removed from the expedition.
// A move within an expedition updates both parties before this hook runs.
// Callers hold the party lock; field exits run separately.
func (s *Server) eventExpeditionChangedLocked() {
	if s.events == nil {
		return
	}
	type expelled struct {
		inst *eventInstance
		peer *partyPeer
	}
	var evict []expelled
	s.events.mu.Lock()
	for id, inst := range s.events.byMember {
		if inst.expeditionID == 0 {
			continue
		}
		p := s.parties.peers[id]
		if p != nil {
			if exp := s.parties.exp.byParty[p.party.ID]; exp != nil && exp.id == inst.expeditionID {
				continue
			}
		}
		delete(s.events.byMember, id)
		delete(inst.members, id)
		evict = append(evict, expelled{inst, p})
	}
	s.events.mu.Unlock()
	if len(evict) != 0 {
		go func() {
			for _, e := range evict {
				s.eventSettle(e.inst)
				if e.peer != nil {
					s.eventExit(e.inst.channel, e.inst.definition, e.peer.live, e.peer.sink)
				}
			}
		}()
	}
}

func (s *Server) expeditionStagePortal(ctx context.Context, snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	switch {
	case portal.Script == "in_cygnus" && (snapshot.Character.MapID == 271040000 || snapshot.Character.MapID == cygnusArena):
		return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 2143004)
	case portal.Script == "portalNPC" &&
		(snapshot.Character.MapID == 211070000 && portal.Name == "lionCastleenter" ||
			snapshot.Character.MapID == 211070100 && portal.Name == "lioncastleout"):
		return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 2161005)
	case portal.Script == "portalNPC1" && snapshot.Character.MapID == 272020110,
		portal.Script == "out_cygnusAK" && snapshot.Character.MapID == 272030400:
		return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 2144018)
	case portal.Script == "Populatus00" && portal.Name == "in00" && snapshot.Character.MapID == content.PapulatusLobby:
		return true, s.startRemoteScript(ctx, snapshot.ChannelID, live, sink, 2043000, "npc/2043000_papulatus.lua", "由時鐘塔入口開啟", 0)
	case portal.Script == "bossMY00" && portal.Name == "boss00" && snapshot.Character.MapID == content.MalaysiaBossLobby:
		return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 9270047)
	case portal.Script == "Zakum05" && (snapshot.Character.MapID == 211042300 || snapshot.Character.MapID == 211042301):
		return true, s.enterZakumLobby(ctx, snapshot, live, sink)
	case portal.Script == "balog_end" && snapshot.Character.MapID == balrogRewardMap:
		return true, s.balrogRewardPortal(ctx, snapshot, live, sink)
	case portal.Script == "in_hillah" && snapshot.Character.MapID == 262030000:
		return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 2184000)
	case portal.Script == "out_hillah":
		if e, ok := content.EventByMap(snapshot.Character.MapID); ok && hillaMode(e.Name) {
			return true, s.openPortalNPC(ctx, snapshot.ChannelID, live, sink, 2184000)
		}
	case portal.Script == "hillah_next":
		mapID := snapshot.Character.MapID
		if e, ok := content.EventByMap(mapID); ok && hillaMode(e.Name) && e.Stage(mapID+100) {
			return true, s.advanceExpedition(ctx, snapshot, live, sink, e.Name, mapID+100)
		}
	}
	return false, nil
}

func (s *Server) advanceExpedition(ctx context.Context, snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, name string, next int) error {
	refuse := func(message string) error {
		_ = sink.WritePacket(protocol.PortalTrainingMessage(message))
		return sink.WritePacket(protocol.EnableActions())
	}
	channel, from := snapshot.ChannelID, snapshot.Character.MapID
	inst := s.eventOf(snapshot.CharacterID)
	if inst == nil || inst.expeditionID == 0 || inst.definition.Name != name || !inst.definition.Stage(next) {
		return refuse("您未加入這個遠征。")
	}
	s.events.mu.Lock()
	open := !inst.starting && !inst.ended && len(inst.members) >= inst.definition.Keep
	s.events.mu.Unlock()
	if !open {
		return refuse("遠征隊還未就緒。")
	}
	if s.mapMonsterCount(channel, from) != 0 {
		return refuse("請先擊倒這一關的守衛。")
	}
	dest, err := s.gameplay.catalog.Map(next)
	if err != nil {
		return refuse("目前無法進入下一關。")
	}
	entry, ok := dest.Spawn()
	if !ok {
		return refuse("目前無法進入下一關。")
	}
	var ready []eventOccupant
	for _, o := range s.eventOccupants(channel, []int{from}) {
		peer := partySession(o.live)
		if peer.CharacterID != snapshot.CharacterID && peer.Character.HP > 0 &&
			s.eventOf(peer.CharacterID) == inst && !s.tradeActive(o.live) {
			ready = append(ready, o)
		}
	}
	if len(ready)+1 < inst.definition.Keep {
		return refuse("請等足夠的隊員抵達或復活，再一起進入下一關。")
	}
	// Move only the admitted expedition roster in this room, even if it is
	// spread across multiple parties. Retain the source field fence.
	for _, o := range append(ready, eventOccupant{live, sink}) {
		s.cancelNPCScriptState(o.live, false)
		s.closeEconomyDialog(o.live)
		_ = s.warpEventMember(ctx, inst, from, o, next, entry)
	}
	return sink.WritePacket(protocol.EnableActions())
}

// Keep the actual scene roster above the minimum as well as the registered
// roster. A half-completed stage change cannot produce a solo clear.
// The caller holds gameplay.mu. No events code holds events.mu while reading
// the gameplay rooms.
func (s *Server) expeditionCombatAllowedLocked(key mapKey, actor int64) bool {
	e, ok := content.EventByMap(key.mapID)
	if !ok || e.ExpeditionType == 0 && e.Name != "Papulatus" && e.Name != content.MalaysiaBossName {
		return true
	}
	if s.events == nil {
		return false
	}
	room := s.gameplay.rooms[key]
	if room == nil {
		return false
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst := s.events.byMember[actor]
	if inst == nil || inst.channel != key.channel || inst.definition.Name != e.Name || inst.starting || inst.ended {
		return false
	}
	present := 0
	for id := range room.members {
		if s.events.byMember[id] == inst {
			present++
		}
	}
	return present >= e.Keep
}

func (s *Server) expeditionBossDefeated(key mapKey, actor int64, mob int) {
	if key.mapID == content.MalaysiaBossArena {
		s.malaysiaBossFormDefeated(key, actor, mob)
		return
	}
	if key.mapID == content.PapulatusArena {
		s.papulatusPartDefeated(key, actor, mob)
		return
	}
	if key.mapID == cygnusArena && mob == cygnusBody {
		s.finishCygnusBattle(key, actor)
		return
	}
	if _, ok := content.ZakumAltarByMap(key.mapID); ok {
		s.zakumPartDefeated(key, actor, mob)
		return
	}
	if encounter, ok := summonedBosses[key.mapID]; ok {
		s.finishSummonedBoss(encounter, key, actor, mob)
		return
	}
	if key.mapID == balrogArena {
		s.balrogPartDefeated(key, actor, mob)
		return
	}
	event, present := content.EventByMap(key.mapID)
	if s.events == nil || !present || !hillaMode(event.Name) ||
		!(key.mapID == 262030300 && mob == 8870000 || key.mapID == 262031300 && mob == 8870100) {
		return
	}
	s.events.mu.Lock()
	inst := s.events.byMember[actor]
	won := inst != nil && inst.definition.Name == event.Name && inst.channel == key.channel &&
		!inst.ended && !inst.starting && !inst.cleared && len(inst.members) >= inst.definition.Keep
	if won {
		inst.cleared = true
	}
	s.events.mu.Unlock()
	if won {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗希拉！拾取獎勵後，可從左側傳送點離開。"))
	}
}

func hillaMode(name string) bool { return name == "Hilla" || name == "HillaHard" }
