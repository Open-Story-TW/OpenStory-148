package world

import (
	"context"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

// Java NPCHandler resolves a native shop before falling back to an NPC script.
// A client can supply only an observed map object ID, never a chosen shop ID.
func (s *Server) openNativeNPCShop(ctx context.Context, channel int, live *liveCharacter, sink packetSink, objectID int32) (bool, error) {
	if s.gameplay == nil {
		return false, nil
	}
	live.mu.Lock()
	character := live.session.Character
	live.mu.Unlock()
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{channel: channel, mapID: character.MapID}]
	var npc content.Life
	var ok bool
	if room != nil {
		npc, ok = room.npcs[objectID]
	}
	s.gameplay.mu.Unlock()
	if !ok {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if !npcReach(character.X, character.Y, npc.X, npc.Y) {
		s.npcGMNotice(live, sink, npc.TemplateID, "未處理(距離太遠 角色 %d,%d NPC %d,%d)", character.X, character.Y, npc.X, npc.Y)
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if s.tradeActive(live) {
		s.npcGMNotice(live, sink, npc.TemplateID, "未處理(交易中不能對話)")
		return true, sink.WritePacket(protocol.EnableActions())
	}
	shop, err := content.NPCShopForNPC(npc.TemplateID)
	if err != nil {
		return false, nil
	}
	s.npcGMNotice(live, sink, npc.TemplateID, "開啟商店 %d (物件 %d)", shop.ID, objectID)
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	return true, s.openNPCShop(ctx, live, sink, npc.TemplateID, shop.ID)
}
