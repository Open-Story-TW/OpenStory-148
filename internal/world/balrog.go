package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const (
	balrogArena          = 105100400
	balrogRewardMap      = 105100401
	balrogWarmupDuration = 3 * time.Minute
	balrogWarmupDamage   = 120000
	balrogRewardEXP      = 260000
	balrogSeal           = 4001261
)

// The opening's damage proves the expedition is active but cannot kill the
// three replicas. At the end, either clear them and use the true WZ HP or
// return the expedition to the lobby. Do not replace a mob still settling.
func (s *Server) startBalrogFight(inst *eventInstance, now time.Time) {
	if inst.definition.Name != "BalrogEasy" || s.gameplay == nil {
		return
	}
	key := mapKey{channel: inst.channel, mapID: balrogArena}
	type replacement struct {
		life  content.Life
		stats content.Mob
	}
	var replacements []replacement
	for _, spawn := range inst.definition.Spawns {
		stats, err := s.gameplay.catalog.Mob(spawn.Life.TemplateID)
		if err != nil {
			s.eventEnd(inst)
			return
		}
		replacements = append(replacements, replacement{spawn.Life, stats})
	}
	g := s.gameplay
	packets, failed := func() ([][]byte, bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		s.events.mu.Lock()
		defer s.events.mu.Unlock()
		if inst.ended || inst.preludeUntil.IsZero() || now.Before(inst.preludeUntil) {
			return nil, false
		}
		room := g.rooms[key]
		if room == nil {
			return nil, true
		}
		var damage int64
		for _, mob := range room.mobs {
			if !mob.prelude {
				continue
			}
			if mob.claiming || mob.reservation != nil {
				return nil, false
			}
			damage += mob.stats.HP - mob.hp
		}
		inst.preludeUntil = time.Time{}
		if len(inst.members) < inst.definition.Keep || damage < balrogWarmupDamage {
			return nil, true
		}
		var packets [][]byte
		for oid, mob := range room.mobs {
			if mob.prelude {
				delete(room.mobs, oid)
				packets = append(packets, protocol.KillMonster(oid))
			}
		}
		for _, row := range replacements {
			oid := g.alloc()
			if oid == 0 {
				return nil, true
			}
			mob := &gameplayMob{objectID: oid, origin: row.life, life: row.life, stats: row.stats, hp: row.stats.HP}
			room.mobs[oid] = mob
			packets = append(packets, mob.spawnPacket(false, now))
		}
		return packets, false
	}()
	if failed {
		s.broadcastInventoryPacket(inst.channel, balrogArena, protocol.PortalTrainingMessage("沒能阻止巴洛古，遠征隊將返回入口。"))
		s.eventEnd(inst)
	}
	if len(packets) > 0 {
		for _, p := range packets {
			s.broadcastInventoryPacket(inst.channel, balrogArena, p)
		}
		s.broadcastInventoryPacket(inst.channel, balrogArena, protocol.PortalTrainingMessage("封印已解除！現在可以擊敗巴洛古。"))
	}
}

func (s *Server) balrogPartDefeated(key mapKey, actor int64, mob int) {
	if s.events == nil || mob < 8830007 || mob > 8830009 {
		return
	}
	inst := func() *eventInstance {
		s.gameplay.mu.Lock()
		defer s.gameplay.mu.Unlock()
		s.events.mu.Lock()
		defer s.events.mu.Unlock()
		inst := s.events.byMember[actor]
		if inst == nil || inst.definition.Name != "BalrogEasy" || inst.channel != key.channel ||
			inst.ended || inst.starting || inst.cleared || !inst.preludeUntil.IsZero() || len(inst.members) < inst.definition.Keep {
			return nil
		}
		room := s.gameplay.rooms[key]
		if room == nil {
			return nil
		}
		for _, m := range room.mobs {
			if m.life.TemplateID >= 8830007 && m.life.TemplateID <= 8830009 && m.hp > 0 {
				return nil
			}
		}
		inst.cleared = true
		inst.deadline = time.Now().Add(10 * time.Minute)
		return inst
	}()
	if inst == nil {
		return
	}
	s.broadcastInventoryPacket(key.channel, balrogArena, protocol.FieldScreenEffect("balog/clear/stone"))
	s.broadcastInventoryPacket(key.channel, balrogArena, protocol.PortalTrainingMessage("已擊敗巴洛古！即將前往獎勵地圖。"))
	time.AfterFunc(5*time.Second, func() { s.warpBalrogReward(inst) })
}

func (s *Server) warpBalrogReward(inst *eventInstance) {
	s.events.mu.Lock()
	cleared := inst.cleared && !inst.ended
	s.events.mu.Unlock()
	if !cleared {
		return
	}
	dest, err := s.gameplay.catalog.Map(balrogRewardMap)
	if err != nil {
		return
	}
	entry, ok := dest.Spawn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, o := range s.eventOccupants(inst.channel, []int{balrogArena}) {
		session := partySession(o.live)
		if s.eventOf(session.CharacterID) != inst {
			continue
		}
		s.cancelNPCScriptState(o.live, false)
		s.closeEconomyDialog(o.live)
		if session.Character.HP == 0 {
			// Reuse the ordinary revival transaction (including buff cancel)
			// to take a defeated member to the cleared reward room.
			guard := func(current domain.CharacterSession) bool { return s.eventOf(current.CharacterID) == inst }
			_ = s.warpGameplayConditional(ctx, inst.channel, o.live, o.sink, dest.ID, entry,
				&revivalTransition{fromMap: balrogArena}, guard, nil)
		} else {
			_ = s.warpEventMember(ctx, inst, balrogArena, o, dest.ID, entry)
		}
	}
}

// Use the same character transaction for the mark, the EXP and the return.
// A full inventory stays in the reward room. A retry may never pay twice.
func (s *Server) balrogRewardPortal(ctx context.Context, original domain.CharacterSession, live *liveCharacter, sink packetSink) error {
	refuse := func(message string) error {
		_ = sink.WritePacket(protocol.PortalTrainingMessage(message))
		return sink.WritePacket(protocol.EnableActions())
	}
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok || s.events == nil || s.tradeActive(live) {
		return refuse("目前無法領取遠征獎勵。")
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	visit := s.sceneFor(live, sink)
	if visit == nil || session.SessionID != original.SessionID || session.Generation != original.Generation ||
		session.ChannelID != original.ChannelID || session.Character.MapID != balrogRewardMap || session.Character.HP <= 0 {
		return nil
	}
	s.events.mu.Lock()
	inst := s.events.byMember[session.CharacterID]
	allowed := inst != nil && inst.definition.Name == "BalrogEasy" && inst.channel == session.ChannelID && inst.cleared && !inst.ended
	s.events.mu.Unlock()
	if !allowed {
		return refuse("必須先完成這次遠征。")
	}
	exit, err := s.gameplay.catalog.Map(inst.definition.ExitMap)
	if err != nil {
		return refuse("目前無法返回入口。")
	}
	entry, ok := exit.Spawn()
	if !ok {
		return refuse("目前無法返回入口。")
	}
	experience := domain.RateExperience(balrogRewardEXP)
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session: session, OperationID: "balrog-" + inst.id + "-reward", EXP: experience,
		Items:  []domain.ScriptItemDelta{{TemplateID: balrogSeal, Quantity: 1}},
		Travel: &domain.ScriptTravel{FromMap: balrogRewardMap, MapID: exit.ID, SpawnPoint: entry.ID, X: entry.X, Y: entry.Y},
	})
	if err != nil {
		return refuse("目前無法領獎，請確認其他欄還有空位後再試。")
	}
	published := session
	published.Character = result.Character
	notify := func() error {
		if result.Replayed {
			return nil
		}
		// publishGameplayWarp already holds the character's scene gate and
		// installed the new visit. Do not lock the old scene a second time.
		for _, packet := range [][]byte{protocol.ModifyInventory(result.Changes),
			protocol.UpdateCombatStats(result.Character), protocol.ShowExperienceGain(experience, true)} {
			if err := sink.WritePacket(packet); err != nil {
				return err
			}
		}
		return nil
	}
	return s.publishGameplayWarp(ctx, session.ChannelID, live, sink, session.Character, published, false, visit, notify, false)
}
