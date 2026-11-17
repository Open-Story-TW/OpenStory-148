package world

import (
	"context"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type dragonRiderState struct {
	mu      sync.Mutex
	rewards map[int64]dragonRiderReward
}

type dragonRiderReward struct {
	item int
	exp  int64
}

const dragonSoaringQuest = 3759
const dragonSoaringScript = "quest/3759.lua"

func dragonSoaringSkill(job int) int {
	switch {
	case domain.IsExplorerJob(job):
		return 1026
	case domain.IsCygnusJob(job):
		return 10001026
	case domain.IsEvanJob(job):
		return 20011026
	case domain.IsMercedesJob(job):
		return 20021026
	case domain.IsPhantomJob(job):
		return 20031026
	case domain.IsAranJob(job):
		return 20001026
	case domain.IsResistanceJob(job):
		return 30001026
	default:
		return 0
	}
}

func knowsSoaring(c domain.Character) bool {
	now := time.Now()
	skill := dragonSoaringSkill(c.Job)
	for _, known := range c.Skills {
		matchesJob := skill > 0 && known.ID == skill || known.ID == 30011026 && domain.IsDemonJob(c.Job)
		if matchesJob && known.Level > 0 && (known.ExpiresAt.IsZero() || known.ExpiresAt.After(now)) {
			return true
		}
	}
	return false
}

func setDragonSoaringSkill(questNPC int, c domain.Character, exchange domain.ScriptExchange) bool {
	if questNPC != 2085000 || c.Level < 100 || questStateOf(c, 3758) != 2 || questStateOf(c, dragonSoaringQuest) != 1 {
		return false
	}
	skill := dragonSoaringSkill(c.Job)
	if skill <= 0 || len(exchange.Growth.Skills) != 1 {
		return false
	}
	// 3759.lua is the only caller: the server assigns the WZ job variant.
	exchange.Growth.Skills[0] = domain.ScriptSkill{ID: skill, Level: 1, Master: 1}
	return true
}

func dragonRiderBox(job int) int {
	// TWMS v148 QuestInfo 1210 and String.wz/Consume provide the five classes.
	switch job / 100 {
	case 2, 12, 22, 32:
		return 2430377 // magician
	case 3, 13, 23, 33:
		return 2430379 // bowman
	case 4, 14, 24:
		return 2430378 // thief
	case 5, 15, 35:
		return 2430380 // pirate
	default:
		return 2430376 // warrior and beginner
	}
}

func (s *Server) dragonRiderPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	room := actor.Character.MapID
	if room == content.DragonRiderExit && p.Name == "out00" && p.Script == "Sky_ReviveOut" {
		m, err := s.gameplay.catalog.Map(content.DragonRiderGate)
		if err != nil {
			return true, err
		}
		entry, ok := m.Spawn()
		if !ok {
			return true, domain.ErrInvalidInput
		}
		s.cancelNPCScript(live)
		s.closeEconomyDialog(live)
		return true, s.warpGameplay(ctx, channel, live, sink, m.ID, entry)
	}
	if room == content.DragonRiderGate && p.Name == "enter00" && p.Script == "Sky_Enter" {
		reason, _, _, err := s.eventStart(ctx, content.DragonRiderName, channel, live, sink)
		if err != nil || reason == "" {
			return true, err
		}
		text := map[string]string{
			"no_party":   "請先組成隊伍。",
			"not_leader": "請讓隊長先進入。",
			"party_size": "需要 3 至 6 名隊員同時在天空之門。",
			"level":      "所有隊員都需要達到 120 級。",
			"soaring":    "所有隊員都必須學會飛翔。",
			"busy":       "已有另一隊正在挑戰。",
		}[reason]
		if text == "" {
			text = "現在無法進入龍騎士任務。"
		}
		return true, dragonRiderNotice(sink, text)
	}
	var next int
	switch {
	case room == content.DragonRiderSky && p.Name == "next00" && p.Script == "Sky_Next":
		next = content.DragonRiderTrap
	case room == content.DragonRiderTrap && p.Name == "boss00" && p.Script == "Sky_Next":
		next = content.DragonRiderArena
	case room == content.DragonRiderArena && p.Name == "out00" && p.Script == "Sky_BossOut":
		next = content.DragonRiderExit
	default:
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.dragon == nil || s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return true, dragonRiderNotice(sink, "請讓隊長通過傳送點。")
	}
	inst.dragon.mu.Lock()
	defer inst.dragon.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) ||
		partySession(live).Character.MapID != room || s.mapMonsterCount(channel, room) != 0 {
		return true, dragonRiderNotice(sink, "請先擊敗這裡的怪物。")
	}
	group := s.dragonRiderGroup(inst, room)
	if len(group) < inst.definition.Keep {
		return true, dragonRiderNotice(sink, "請等隊員抵達再一起前進。")
	}
	if next == content.DragonRiderExit {
		return true, s.finishDragonRider(ctx, inst, group)
	}
	m, err := s.gameplay.catalog.Map(next)
	if err != nil {
		return true, err
	}
	entry, ok := m.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	return true, s.moveEventRoom(ctx, inst, room, eventOccupant{live, sink}, next, entry)
}

func dragonRiderNotice(sink packetSink, text string) error {
	if err := sink.WritePacket(protocol.PortalTrainingMessage(text)); err != nil {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func (s *Server) dragonRiderGroup(inst *eventInstance, mapID int) []eventOccupant {
	members := s.eventOccupants(inst.channel, []int{mapID})
	group := members[:0]
	for _, member := range members {
		peer := partySession(member.live)
		if peer.Character.HP > 0 && s.eventOf(peer.CharacterID) == inst {
			group = append(group, member)
		}
	}
	return group
}

func (s *Server) finishDragonRider(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	if len(inst.dragon.rewards) == 0 {
		for _, member := range group {
			peer := partySession(member.live)
			level := max(1, peer.Character.Level)
			scaled := level
			if level > 200 {
				scaled = 200 + level/10
			}
			denominator := int64(3 * min(200, level))
			// Legacy Sky_BossOut awards level-scaled PQ experience.
			inst.dragon.rewards[peer.CharacterID] = dragonRiderReward{
				item: dragonRiderBox(peer.Character.Job),
				exp:  (content.ExperienceForLevel(scaled)*20 + denominator/2) / denominator,
			}
		}
	}
	for _, member := range group {
		peer := partySession(member.live)
		reward, ok := inst.dragon.rewards[peer.CharacterID]
		if !ok {
			continue
		}
		err := s.applyEventExchange(ctx, member, "dragon-rider-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(session.CharacterID) != inst || session.Character.MapID != content.DragonRiderArena {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{
				EXP:   reward.exp,
				Items: []domain.ScriptItemDelta{{TemplateID: reward.item, Quantity: 1}},
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "dragon-rider-clear"}, Delta: 1}},
			}, nil
		})
		if err != nil {
			return err
		}
	}
	s.broadcastInventoryPacket(inst.channel, content.DragonRiderArena, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.broadcastInventoryPacket(inst.channel, content.DragonRiderArena, protocol.FieldSoundEffect(eventEffects["clear"][1]))
	s.eventEndAt(inst, content.DragonRiderExit)
	return nil
}
