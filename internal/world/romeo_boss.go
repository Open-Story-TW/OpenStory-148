package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type romeoBossRoom struct {
	boss, escort *gameplayMob
	result       bool
}

// These NPCs have event-specific native dialogue even if no Lua source exists.
func romeoDialogueNPC(id int) bool {
	switch id {
	case 2112000, 2112001, 2112002, 2112006, 2112007, 2112008, 2112009, 2112010, 2112011, 2112012, 2112013, 2112018:
		return true
	}
	return false
}

func (s *Server) tickRomeoBossResults() {
	g := s.gameplay
	g.mu.Lock()
	var updates []roomDelivery
	for key, room := range g.rooms {
		stage := room.romeoBoss
		if stage == nil || stage.result || stage.boss.hp > 0 || stage.boss.claiming || stage.boss.reservation != nil {
			continue
		}
		after := g.rooms[mapKey{key.channel, key.mapID + 99}]
		reward := g.rooms[mapKey{key.channel, key.mapID + 199}]
		if after == nil || reward == nil {
			continue
		}
		lobby, wise, defeated := 2112004, 2112002, 2112001
		if key.mapID == 926110401 {
			lobby, wise, defeated = 2112003, 2112012, 2112011
		}
		arenaNPC := g.alloc()
		afterNPC := g.alloc()
		rewardNPC := g.alloc()
		if arenaNPC == 0 || afterNPC == 0 || rewardNPC == 0 {
			continue
		}
		yurete, claimant := wise, 2112018
		if stage.escort.hp == 0 {
			yurete, claimant = defeated, 2112008
			if key.mapID == 926110401 {
				claimant = 2112009
			}
		}
		release := content.Life{Kind: "n", TemplateID: lobby, X: -416, Y: stage.escort.origin.Y, Foothold: stage.escort.origin.Foothold}
		room.npcs[arenaNPC] = release
		after.npcs[afterNPC] = content.Life{Kind: "n", TemplateID: yurete, X: 232, Y: 150, Foothold: 3}
		reward.npcs[rewardNPC] = content.Life{Kind: "n", TemplateID: claimant, X: 111, Y: 128, Foothold: 12}
		stage.result = true
		for _, p := range s.npcScriptability(map[int32]content.Life{arenaNPC: release}) {
			updates = append(updates, roomBroadcast(room, p, -1)...)
		}
		updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(arenaNPC, release, false), -1)...)
		updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(arenaNPC, release, true), -1)...)
		for _, p := range [][]byte{protocol.FieldScreenEffect(eventEffects["clear"][0]), protocol.FieldSoundEffect(eventEffects["clear"][1])} {
			updates = append(updates, roomBroadcast(room, p, -1)...)
		}
	}
	g.mu.Unlock()
	deliver(updates)
}

func (s *Server) enterRomeoBoss(ctx context.Context, inst *eventInstance, actor eventOccupant, session domain.CharacterSession) error {
	current := session.Character.MapID
	if len(s.activeEventMembers(inst, current)) != inst.definition.Keep ||
		s.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID {
		return eventPortalNotice(actor.sink, "請隊長等四人集合後，再一起前往中央研究室。")
	}
	s.events.mu.Lock()
	bossID := 9300139
	if inst.romeoAngered {
		bossID = 9300140
	}
	s.events.mu.Unlock()
	g := s.gameplay
	destination, err := g.catalog.Map(current + 1)
	if err != nil {
		return err
	}
	entry, ok := destination.Portal("st00")
	if !ok {
		return domain.ErrNotFound
	}
	stats, err := g.catalog.Mob(bossID)
	if err != nil {
		return err
	}
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, destination.ID}]
	life, grounded := romeoSpawnGround(destination, 240, 150)
	if room == nil || !grounded {
		g.mu.Unlock()
		return domain.ErrNotFound
	}
	if room.romeoBoss == nil {
		for _, mob := range room.mobs {
			if mob.life.TemplateID == 9300137 || mob.life.TemplateID == 9300138 {
				mob.stats.Invincible, mob.oneTime = true, true
				if boss, _ := g.spawnEventBossForm(room, life, stats, session.CharacterID, time.Now()); boss != nil {
					room.romeoBoss = &romeoBossRoom{boss: boss, escort: mob}
				}
				break
			}
		}
	}
	ready := room.romeoBoss != nil
	g.mu.Unlock()
	if !ready {
		return domain.ErrNotFound
	}
	return s.moveEventRoom(ctx, inst, current, actor, destination.ID, entry)
}

func (s *Server) romeoEscortHit(channel int, live *liveCharacter, hit protocol.MobHitEscort, now time.Time) bool {
	session := partySession(live)
	mapID := session.Character.MapID
	if mapID != 926100401 && mapID != 926110401 {
		return false
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.channel != channel || int64(hit.CharacterID) != session.CharacterID {
		return true
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, mapID}]
	if room == nil || room.members[session.CharacterID] == nil || room.romeoBoss == nil {
		g.mu.Unlock()
		return true
	}
	fighter, escort := room.mobs[hit.Attacker], room.romeoBoss.escort
	if fighter == nil || fighter.life.TemplateID != 9300150 || fighter.controller != session.CharacterID ||
		fighter.hp <= 0 || escort.hp <= 0 || room.romeoBoss.boss.hp <= 0 || escort.objectID != hit.Escort ||
		!within(fighter.life.X, fighter.life.Y, escort.life.X, escort.life.Y, 200) ||
		now.Sub(fighter.escortHitAt) < time.Second {
		g.mu.Unlock()
		return true
	}
	fighter.escortHitAt = now
	damage := min(escort.hp, int64(max(1, fighter.stats.PAD)))
	escort.hp -= damage
	updates := roomBroadcast(room, protocol.DamageEscort(escort.objectID, int(damage), int(escort.hp), int(escort.stats.HP)), -1)
	if escort.hp == 0 {
		updates = append(updates, roomBroadcast(room, protocol.KillMonster(escort.objectID), -1)...)
		updates = append(updates, roomBroadcast(room, protocol.PortalTrainingMessage("護送對象倒下了，仍可繼續擊退法郎肯洛伊德。"), -1)...)
	}
	g.mu.Unlock()
	deliver(updates)
	return true
}

func (s *Server) romeoFinalNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	session := partySession(live)
	mapID := session.Character.MapID
	switch mapID {
	case 926100401, 926110401, 926100500, 926110500, 926100600, 926110600:
	default:
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.channel != channel || !inst.definition.Stage(mapID) ||
		session.Character.HP <= 0 || s.tradeActive(live) {
		return false, nil
	}
	g := s.gameplay
	first := inst.definition.Maps[0]
	g.mu.Lock()
	room, arena := g.rooms[mapKey{channel, mapID}], g.rooms[mapKey{channel, first + 401}]
	var npc content.Life
	if room != nil {
		npc = room.npcs[oid]
	}
	bossReady := arena != nil && arena.romeoBoss != nil && arena.romeoBoss.boss.hp == 0 &&
		!arena.romeoBoss.boss.claiming && arena.romeoBoss.boss.reservation == nil
	g.mu.Unlock()
	if !bossReady || npc.TemplateID == 0 || !within(session.Character.X, session.Character.Y, npc.X, npc.Y, 150) {
		return false, nil
	}
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	actor := eventOccupant{live, sink}
	switch mapID - first {
	case 401, 500:
		if s.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID ||
			len(s.activeEventMembers(inst, mapID)) != inst.definition.Keep {
			return true, eventPortalNotice(sink, "請等隊長與四人集合後再繼續。")
		}
		next := first + 500
		if mapID == next {
			next = first + 600
		}
		destination, err := g.catalog.Map(next)
		if err != nil {
			return true, err
		}
		entry, ok := destination.Spawn()
		if !ok {
			return true, domain.ErrNotFound
		}
		return true, s.moveEventRoom(ctx, inst, mapID, actor, next, entry)
	case 600:
		if npc.TemplateID == 9000033 {
			return false, nil
		}
		err := s.romeoClaimReward(ctx, inst, actor, mapID)
		return true, err
	}
	return false, nil
}

func (s *Server) romeoClaimReward(ctx context.Context, inst *eventInstance, actor eventOccupant, mapID int) error {
	marble := 4001159
	if inst.definition.Name == content.JulietPQName {
		marble = 4001160
	}
	s.events.mu.Lock()
	exp := int64(105000)
	if inst.romeoAngered {
		exp = 140000
	}
	s.events.mu.Unlock()
	err := s.applyEventExchange(ctx, actor, "romeo-clear-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		items := append(romeoSuppliesToRemove(current.Character), domain.ScriptItemDelta{TemplateID: marble, Quantity: 1})
		return domain.ScriptExchange{
			Items: items,
			EXP:   exp,
			Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "romeo-and-juliet-clear"}, Delta: 1}},
		}, nil
	})
	if errors.Is(err, domain.ErrInsufficient) {
		return eventPortalNotice(actor.sink, "請先在其他欄位留一個空位。")
	}
	if err != nil {
		return err
	}
	s.events.mu.Lock()
	inst.cleared = true
	s.events.mu.Unlock()
	return s.romeoWarpNamed(ctx, inst, actor, mapID, inst.definition.ExitMap, "sp")
}
