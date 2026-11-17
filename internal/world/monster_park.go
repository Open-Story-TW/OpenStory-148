package world

import (
	"context"
	"slices"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// monsterParkPortal implements the stage gates and the final reward. The
// portal scripts in Map.wz explicitly distinguish the last of the six maps.
func (s *Server) monsterParkPortal(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	if portal.Script != "mPark_nextStage" && portal.Script != "mPark_final" {
		return false, nil
	}
	notice := func(text string) (bool, error) {
		_ = sink.WritePacket(protocol.PortalTrainingMessage(text))
		return true, sink.WritePacket(protocol.EnableActions())
	}
	mapID := actor.Character.MapID
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.definition.Ticket == 0 {
		return notice("請先組隊進入怪物公園。")
	}
	if inst.partyID != 0 && s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return notice("請由隊長使用這個傳點。")
	}
	if s.mapMonsterCount(inst.channel, mapID) > 0 {
		return notice("請先打倒這一關的所有怪物。")
	}
	stage := slices.Index(inst.definition.Maps, mapID)
	if stage < 0 {
		return true, domain.ErrInvalidInput
	}
	final := stage == len(inst.definition.Maps)-1
	if final != (portal.Script == "mPark_final") {
		return true, domain.ErrInvalidInput
	}
	group := []eventOccupant{{live, sink}}
	for _, other := range s.eventOccupants(inst.channel, []int{mapID}) {
		there := partySession(other.live)
		if there.CharacterID != actor.CharacterID && there.Character.HP > 0 && !s.tradeActive(other.live) &&
			s.eventOf(there.CharacterID) == inst {
			group = append(group, other)
		}
	}
	s.events.mu.Lock()
	wholeParty := !inst.ended && len(group) == len(inst.members) && len(group) >= inst.definition.Keep
	s.events.mu.Unlock()
	if !wholeParty {
		return notice("請先等隊員到齊。")
	}
	if final {
		if err := s.exchangeEventItems(ctx, inst, group, mapID, "reward", 4310020, 1); err != nil {
			return notice("目前無法發放獎勵，請確認每位隊員的背包有空位。")
		}
		s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldScreenEffect("quest/party/clear"))
		s.eventEnd(inst)
		return true, sink.WritePacket(protocol.EnableActions())
	}
	next, err := s.gameplay.catalog.Map(inst.definition.Maps[stage+1])
	if err != nil {
		return true, err
	}
	entry, ok := next.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	for _, member := range group[1:] {
		s.cancelNPCScriptState(member.live, false)
		s.closeEconomyDialog(member.live)
		if err := s.warpEventMember(ctx, inst, mapID, member, next.ID, entry); err != nil {
			s.eventEnd(inst)
			return notice("有隊員傳送失敗，隊伍將返回等候地圖。")
		}
	}
	if err := s.warpEventMember(ctx, inst, mapID, group[0], next.ID, entry); err != nil {
		s.eventEnd(inst)
		return notice("隊長傳送失敗，隊伍將返回等候地圖。")
	}
	return true, nil
}
