package world

import (
	"context"
	"errors"
	"fmt"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const ravanaDailyLimit int64 = 3

var ravanaDaily = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogDaily, Name: "ravana_entries"}

func ravanaHasEntry(c domain.Character) bool {
	return questStateOf(c, content.RavanaStoryQuest) == 2 && questStateOf(c, content.RavanaEntryQuest) == 2
}

func ravanaCanTurnIn(c domain.Character, questID, itemID int) bool {
	if questStateOf(c, questID) != 1 {
		return false
	}
	for _, item := range c.Items {
		if item.TemplateID == itemID && item.Quantity > 0 {
			return true
		}
	}
	return false
}

func (s *Server) craftRavanaSpark(ctx context.Context, live *liveCharacter, sink packetSink, q domain.UseItemRequest) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	actor := s.skillSession(live)
	if err := assetOutcomeUncertain(live, actor); err != nil {
		return err
	}
	if s.gmBusy(live) || s.tradeActive(live) || actor.Character.HP <= 0 || questStateOf(actor.Character, content.RavanaEntryQuest) != 1 {
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	ownsSlot := false
	for _, item := range actor.Character.Items {
		if item.InvType == 2 && item.Slot == q.Slot && item.TemplateID == content.RavanaBurning && item.Quantity > 0 {
			ownsSlot = true
			break
		}
	}
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ownsSlot || !ok {
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session: actor, OperationID: fmt.Sprintf("ravana-spark-%s-%d-%d", actor.SessionID, q.ClientTick, q.Slot),
		Items: []domain.ScriptItemDelta{
			{TemplateID: content.RavanaBurning, Quantity: -10},
			{TemplateID: content.RavanaSpark, Quantity: 1},
		},
	})
	if errors.Is(err, domain.ErrInsufficient) {
		return s.writeScenePackets(visit, protocol.PortalTrainingMessage("合成太陽火花需要 10 個燃燒的太陽火花與一格其他欄空間。"), protocol.EnableActions())
	}
	if err != nil {
		return err
	}
	s.applyInventoryResult(live, result.Character)
	if !result.Replayed {
		return s.writeScenePackets(visit, protocol.ModifyInventory(result.Changes), protocol.ShowItemGain(content.RavanaSpark, 1), protocol.EnableActions())
	}
	return s.writeScenePackets(visit, protocol.EnableActions())
}

func (s *Server) startRavana(ctx context.Context, inst *eventInstance, rosterSize int) error {
	group := s.activeEventMembers(inst, content.RavanaArena)
	if len(group) != rosterSize {
		return domain.ErrStaleOwner
	}
	limit := ravanaDailyLimit
	return s.exchangeEventRosterLog(ctx, inst, group, content.RavanaArena, "ravana-entry",
		domain.ScriptLogChange{Key: ravanaDaily, Delta: 1, Maximum: &limit},
		domain.ScriptQuestForce{QuestID: content.RavanaEntryQuest, State: 1})
}

func (s *Server) ravanaPortal(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	c := actor.Character
	if portal.Type != 7 {
		return false, nil
	}
	warp := func(mapID int, entryName string) error {
		dest, err := s.gameplay.catalog.Map(mapID)
		if err != nil {
			return err
		}
		entry, ok := dest.Spawn()
		if entryName != "" {
			entry, ok = dest.Portal(entryName)
		}
		if !ok {
			return domain.ErrNotFound
		}
		s.cancelNPCScriptState(live, false)
		s.closeEconomyDialog(live)
		return s.warpGameplayConditional(ctx, actor.ChannelID, live, sink, mapID, entry, nil, func(current domain.CharacterSession) bool {
			return current.Character.MapID == c.MapID && current.Character.HP > 0
		}, nil)
	}
	switch {
	case c.MapID == 252010300 && portal.Name == "in00" && portal.Script == "goldTemple_sGate":
		if questStateOf(c, 3856) == 0 {
			return true, eventPortalNotice(sink, "請先向守衛兵詢問廢棄寺廟的老方丈。")
		}
		return true, warp(content.RavanaAbandoned, "out00")
	case c.MapID == content.RavanaAbandoned && portal.Name == "boss00" && portal.Script == "gold_boss_gate":
		if questStateOf(c, 3860) != 2 {
			return true, eventPortalNotice(sink, "請先完成老方丈的考驗。")
		}
		return true, warp(content.RavanaTrial, "st00")
	case c.MapID == content.RavanaLobby && portal.Name == "out00" && portal.Script == "waitOut":
		return true, warp(content.RavanaTrial, "st00")
	case c.MapID == content.RavanaTemple && portal.Name == "out00" && portal.Script == "goldTemple_out":
		return true, warp(251000000, "")
	case c.MapID == content.RavanaLobby && portal.Name == "boss00" && portal.Script == "goldTempleBoss":
		return true, s.enterRavana(ctx, actor, live, sink)
	default:
		return false, nil
	}
}

func (s *Server) enterRavana(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink) error {
	name := content.RavanaName
	if questStateOf(actor.Character, content.RavanaStoryQuest) == 1 {
		name = content.RavanaStoryName
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	reason, _, _, err := s.eventStart(ctx, name, actor.ChannelID, live, sink)
	if err != nil || reason == "" {
		return err
	}
	message := map[string]string{
		"no_party":     "請與夥伴一起挑戰六手邪神。",
		"not_leader":   "請由隊長進入祭壇。",
		"party_size":   "請讓 2 至 6 位隊員在祭壇入口集合。",
		"level":        "全隊須達 115 級。",
		"daily":        "隊伍中有人已達每日挑戰上限。",
		"ravana_spark": "全隊須完成惡魔六手邪神，並交給考驗之門一個太陽火花。",
		"busy":         "祭壇內有人正在挑戰，請稍候。",
	}
	if name == content.RavanaStoryName && (reason == "party_size" || reason == "not_leader") {
		return eventPortalNotice(sink, "請先獨自完成瑪妮交付的試煉。")
	}
	if text := message[reason]; text != "" {
		return eventPortalNotice(sink, text)
	}
	return eventPortalNotice(sink, "目前無法進入祭壇。")
}
