package world

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"maple-go-server/scripts"
)

func (s *Server) questNPC(live *liveCharacter, templateID int) (int32, bool) {
	if s.gameplay == nil {
		return 0, false
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	room := s.gameplay.rooms[mapKey{channel: session.ChannelID, mapID: session.Character.MapID}]
	if room == nil {
		return 0, false
	}
	for oid, npc := range room.npcs {
		if npc.TemplateID == templateID && npcReach(session.Character.X, session.Character.Y, npc.X, npc.Y) {
			return oid, true
		}
	}
	return 0, false
}

func (s *Server) changeQuest(ctx context.Context, live *liveCharacter, sink packetSink, request domain.QuestRequest) (domain.QuestResult, error) {
	repo, ok := s.repo.(domain.QuestRepository)
	if !ok {
		return domain.QuestResult{}, domain.ErrForbidden
	}
	if s.tradeActive(live) {
		return domain.QuestResult{}, domain.ErrForbidden
	}
	if request.Action != "forfeit" {
		definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), request.QuestID)
		remote := err == nil && request.Script == "" && definition.RemoteStart(request.Action)
		if _, valid := s.questNPC(live, request.NPCID); !valid && !remote {
			return domain.QuestResult{}, domain.ErrForbidden
		}
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	request.Session = live.session
	live.mu.Unlock()
	value, err := repo.TransitionQuest(ctx, request)
	if err != nil {
		return value, err
	}
	s.applyProgressResult(live, value.Character)
	if !value.Replayed {
		s.applyItemBuff(live, sink, domain.ItemUseResult{Character: value.Character, Buffs: value.Buffs, Replaced: value.Replaced})
	}
	beforeTraits := request.Session.Character.Traits
	if value.Character.Traits != beforeTraits {
		for _, packet := range [][]byte{protocol.TraitStats(request.Session.Character, value.Character), protocol.TraitGain(beforeTraits, value.Character.Traits)} {
			if packet != nil {
				if err = sink.WritePacket(packet); err != nil {
					return value, err
				}
			}
		}
	}
	if len(value.Changes) > 0 {
		if err = sink.WritePacket(protocol.ModifyInventory(value.Changes)); err != nil {
			return value, err
		}
	}
	if value.Character.Fame != request.Session.Character.Fame {
		if err = sink.WritePacket(protocol.UpdateFame(value.Character.Fame)); err != nil {
			return value, err
		}
	}
	for _, packet := range questResultPackets(value, request.NPCID) {
		if err = sink.WritePacket(packet); err != nil {
			return value, err
		}
	}
	live.mu.Lock()
	current := live.session
	live.mu.Unlock()
	if err = s.refreshQuestDrops(ctx, current, sink); err != nil {
		return value, err
	}
	return value, nil
}

func questResultPackets(value domain.QuestResult, npcID int) [][]byte {
	packets := [][]byte{protocol.QuestUpdate(value.Quest), protocol.UpdateCombatStats(value.Character), protocol.UpdateMesos(value.Character.Mesos, true)}
	// Native UPDATE_QUEST_INFO is an action notification, not a quest-state
	// field. Java sends action 10 after normal acceptance or for a next quest.
	if value.Quest.State == 1 || value.Quest.State == 2 && value.NextQuest > 0 {
		packets = append(packets, protocol.QuestInfo(value.Quest.ID, npcID, 10, value.NextQuest))
	}
	return packets
}

func (s *Server) applyProgressResult(live *liveCharacter, value domain.Character) {
	s.applyInventoryResult(live, value)
	live.mu.Lock()
	c := &live.session.Character
	jobChanged := value.Revision == c.Revision && value.Job != c.Job
	if value.Revision == c.Revision {
		c.Job = value.Job
		c.Level = value.Level
		c.EXP = value.EXP
		c.STR = value.STR
		c.DEX = value.DEX
		c.INT = value.INT
		c.LUK = value.LUK
		c.AP = value.AP
		c.SP = value.SP
		c.Fame = value.Fame
		c.Quests = value.Quests
		c.QuestRecords = value.QuestRecords
		c.Traits = value.Traits
	}
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	if jobChanged {
		s.refreshDragon(snapshot.ChannelID, snapshot.Character.MapID, snapshot.CharacterID, snapshot.Character.Job)
	}
	s.updateQuestDropSession(snapshot)
	s.refreshParty(live)
}

func (s *Server) handleQuest(ctx context.Context, channel int, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	request, err := protocol.ReadQuestAction(r)
	if err != nil {
		slog.Warn("quest_packet_rejected", "action", request.Action, "quest", request.QuestID, "remaining", r.Remaining(), "error", err)
		return err
	}
	live.mu.Lock()
	before := questStateOf(live.session.Character, request.QuestID)
	live.mu.Unlock()
	if request.ItemSlot != 0 && request.ItemSlot != -1 {
		// Cash-assisted quest transitions need a dedicated atomic consume path.
		s.questGMNotice(live, sink, request, before, "未處理(現金道具輔助的任務尚未支援)")
		return sink.WritePacket(protocol.EnableActions())
	}
	if request.QuestID == dailyResetQuest && (request.Action == 1 || request.Action == 4) {
		return s.completeDailyResetQuest(ctx, live, sink, request, before)
	}
	if request.Action == 4 || request.Action == 5 {
		// Only reviewed source mappings can launch a quest script.
		name := fmt.Sprintf("quest/%d.lua", request.QuestID)
		if _, missing := scripts.Source(name); missing != nil {
			s.questGMNotice(live, sink, request, before, fmt.Sprintf("未處理(伺服器沒有 %s 腳本)", name))
			return sink.WritePacket(protocol.EnableActions())
		}
		oid, ok := s.questNPC(live, request.NPCID)
		// The client starts a normalAutoStart quest wherever the character
		// stands (the medal quests of NPC 9000040), so its generated start
		// script runs without the NPC; completion still happens at the NPC.
		remote := !ok && request.Action == 4 && scripts.LegacyQuest(name) && s.questAutoStart(request.QuestID, request.NPCID)
		if !ok && !remote {
			s.questGMNotice(live, sink, request, before, "未處理(附近找不到這個 NPC)")
			return sink.WritePacket(protocol.EnableActions())
		}
		if scripts.LegacyQuest(name) {
			if reason := s.legacyQuestBlocked(live, request, name); reason != "" {
				s.questGMNotice(live, sink, request, before, "未處理("+reason+")")
				return sink.WritePacket(protocol.EnableActions())
			}
		}
		s.questGMNotice(live, sink, request, before, "啟動腳本 "+name)
		if remote {
			return s.startRemoteScript(ctx, channel, live, sink, request.NPCID, name, "自動開始的任務", 0)
		}
		_, err = s.startScriptAtNPC(ctx, channel, live, sink, oid, name)
		return err
	}
	action := map[uint8]string{1: "start", 2: "complete", 3: "forfeit"}[request.Action]
	_, err = s.changeQuest(ctx, live, sink, domain.QuestRequest{QuestID: request.QuestID, NPCID: request.NPCID, Action: action, OperationID: "quest-native-" + platform.NewID(), Selection: request.Selection})
	if err != nil {
		s.questGMNotice(live, sink, request, before, "失敗("+err.Error()+")")
		return sink.WritePacket(protocol.EnableActions())
	}
	s.questGMNotice(live, sink, request, before, "成功")
	return nil
}

// questAutoStart reports whether the WZ data lets the client offer this quest
// away from its start NPC.
func (s *Server) questAutoStart(questID, npcID int) bool {
	definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), questID)
	return err == nil && definition.Start.AutoStart && definition.Start.NPCID == npcID
}

// legacyQuestBlocked is Legacy NPCScriptManager.startQuest/endQuest: a start
// script runs only while MapleQuest.canStart holds and an end script only
// while canComplete holds. Generated state machines force quest status and
// grant rewards themselves, so this check is what stops a client from
// replaying a finished quest's script.
func (s *Server) legacyQuestBlocked(live *liveCharacter, request protocol.QuestActionRequest, script string) string {
	definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), request.QuestID)
	if err != nil {
		return "無任務資料"
	}
	if len(definition.Unsupported) > 0 {
		return "任務含未支援項目"
	}
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	state := domain.QuestState{ID: request.QuestID}
	for _, q := range c.Quests {
		if q.ID == request.QuestID {
			state = q
		}
	}
	requirement, want := definition.Start, 0
	if request.Action == 5 {
		requirement, want = definition.Complete, 1
	}
	if state.State != want && !(want == 0 && state.State == 2 && requirement.Interval > 0) {
		return "任務狀態不符"
	}
	if requirement.Script != "" {
		requirement.Script = script
	}
	if err = content.QuestRequirementMet(c, state, requirement, request.NPCID, script, time.Now()); err != nil {
		return "未達成任務條件"
	}
	return ""
}
