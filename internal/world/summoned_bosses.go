package world

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type summonedBoss struct {
	event, title string
	npc, intro   int
	body, fh     int
	victoryNPCs  []content.Life
}

var summonedBosses = map[int]summonedBoss{
	211070100: {"VonLeon", "凡雷恩", 2161000, 8840010, 8840000, 1, []content.Life{
		{Kind: "n", TemplateID: 2161008, X: -25, Y: -181, Foothold: 1, RX0: -75, RX1: 25},
		{Kind: "n", TemplateID: 2161009, X: 25, Y: -181, Foothold: 1, RX0: -25, RX1: 75},
	}},
	272030400: {"Arkarium", "阿卡伊農", 2144010, 8860010, 8860000, 8, nil},
}

// summonExpeditionBoss consumes the exact challenge NPC as a scene action.
// Only a member in the right arena may trigger it, and the registered group
// must still have at least the minimum number of members in that room.
func (s *Server) summonExpeditionBoss(inst *eventInstance, actor domain.CharacterSession, npc int, oid int32) bool {
	if s.gameplay == nil || s.events == nil {
		return false
	}
	mode, ok := summonedBosses[actor.Character.MapID]
	if !ok || mode.event != inst.definition.Name || mode.npc != npc || oid == 0 {
		return false
	}
	g := s.gameplay
	introStats, err := g.catalog.Mob(mode.intro)
	if err != nil {
		return false
	}
	bodyStats, err := g.catalog.Mob(mode.body)
	if err != nil {
		return false
	}
	key := mapKey{channel: actor.ChannelID, mapID: actor.Character.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || !s.expeditionCombatAllowedLocked(key, actor.CharacterID) || room.npcs[oid].TemplateID != npc {
		g.mu.Unlock()
		return false
	}
	for _, m := range room.mobs {
		if m.hp > 0 || m.claiming {
			g.mu.Unlock()
			return false
		}
	}
	s.events.mu.Lock()
	if inst.summoned || inst.ended || s.events.byMember[actor.CharacterID] != inst {
		s.events.mu.Unlock()
		g.mu.Unlock()
		return false
	}
	makeMob := func(stats content.Mob, fake bool) *gameplayMob {
		life := content.Life{Kind: "m", TemplateID: stats.ID, X: 0, Y: -181, Foothold: mode.fh, RespawnSeconds: -1}
		return &gameplayMob{objectID: g.alloc(), life: life, origin: life, stats: stats, hp: stats.HP, fake: fake}
	}
	opener := makeMob(introStats, true)
	boss := makeMob(bodyStats, false)
	if opener.objectID == 0 || boss.objectID == 0 {
		s.events.mu.Unlock()
		g.mu.Unlock()
		return false
	}
	inst.summoned = true
	delete(room.npcs, oid)
	room.mobs[opener.objectID] = opener
	s.events.mu.Unlock()
	g.mu.Unlock()
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.RemoveNPC(oid, true))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.RemoveNPC(oid, false))
	s.broadcastInventoryPacket(key.channel, key.mapID, opener.spawnPacket(false, time.Now()))
	// Both intro templates specify removeAfter=1 and revive to the real
	// boss. This is animation only, so it produces no defeat or loot row.
	time.AfterFunc(time.Second, func() {
		g.mu.Lock()
		s.events.mu.Lock()
		active := g.rooms[key] == room && room.mobs[opener.objectID] == opener &&
			!inst.ended && len(inst.members) >= inst.definition.Keep
		if active {
			delete(room.mobs, opener.objectID)
			room.mobs[boss.objectID] = boss
		}
		s.events.mu.Unlock()
		g.mu.Unlock()
		if active {
			s.broadcastInventoryPacket(key.channel, key.mapID, protocol.RemoveMonster(opener.objectID))
			s.broadcastInventoryPacket(key.channel, key.mapID, boss.spawnPacket(false, time.Now()))
		}
	})
	return true
}

func (s *Server) finishSummonedBoss(mode summonedBoss, key mapKey, actor int64, mob int) {
	if mob != mode.body || s.events == nil || s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	s.events.mu.Lock()
	inst := s.events.byMember[actor]
	won := inst != nil && inst.definition.Name == mode.event && inst.summoned && !inst.ended && !inst.cleared
	added := map[int32]content.Life{}
	if won {
		inst.cleared = true
		for _, life := range mode.victoryNPCs {
			if oid := g.alloc(); oid != 0 {
				room.npcs[oid] = life
				added[oid] = life
			}
		}
	}
	s.events.mu.Unlock()
	g.mu.Unlock()
	if !won {
		return
	}
	for _, p := range s.npcScriptability(added) {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	for id, life := range added {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.SpawnNPC(id, life, false))
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.SpawnNPC(id, life, true))
	}
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗"+mode.title+"！請拾取獎勵，再從出口離開。"))
}
