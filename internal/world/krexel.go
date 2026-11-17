package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const krexelLastReactorState = 6

type krexelBattle struct {
	altar   *roomReactor
	lastHit time.Time
	forms   [3]*gameplayMob
	stage   int
}

func (s *Server) enterKrexel(ctx context.Context, snapshot domain.CharacterSession, live *liveCharacter, sink packetSink) error {
	g := s.gameplay
	if g == nil || snapshot.Character.HP <= 0 {
		return sink.WritePacket(protocol.EnableActions())
	}
	g.mu.Lock()
	room, err := g.room(mapKey{channel: snapshot.ChannelID, mapID: content.KrexelArena})
	if err != nil {
		g.mu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	entry, ok := room.definition.Spawn()
	battle := room.krexel
	fighting := battle != nil && battle.altar.state == krexelLastReactorState && battle.stage < len(battle.forms)
	g.mu.Unlock()
	if fighting {
		if err := sink.WritePacket(protocol.PortalTrainingMessage("克雷賽爾戰鬥已經開始，請等候下一場。")); err != nil {
			return err
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	return s.warpGameplayConditional(ctx, snapshot.ChannelID, live, sink, content.KrexelArena, entry, nil, func(current domain.CharacterSession) bool {
		return current.Character.MapID == content.KrexelLobby && current.Character.HP > 0
	}, nil)
}

func (s *Server) hitKrexelReactor(channel int, live *liveCharacter, oid int32) bool {
	live.mu.Lock()
	character := live.session.Character
	live.mu.Unlock()
	if character.MapID != content.KrexelArena {
		return false
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: content.KrexelArena}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.reactors[oid] == nil || room.reactors[oid].def.TemplateID != content.KrexelReactor {
		g.mu.Unlock()
		return false
	}
	altar := room.reactors[oid]
	member := room.members[character.ID]
	if member == nil || member.live != live || character.HP <= 0 || altar.state >= krexelLastReactorState ||
		abs(character.X-altar.def.X) > 160 || abs(character.Y-altar.def.Y) > 125 {
		g.mu.Unlock()
		return true
	}
	if room.krexel == nil {
		room.krexel = &krexelBattle{altar: altar}
	}
	battle := room.krexel
	now := time.Now()
	if now.Sub(battle.lastHit) < 300*time.Millisecond {
		g.mu.Unlock()
		return true
	}
	lastHit := altar.state == krexelLastReactorState-1
	if lastHit {
		for phase := range battle.forms {
			stats, err := g.catalog.Mob(9420520 + phase)
			objectID := g.alloc()
			if err != nil || objectID == 0 {
				g.mu.Unlock()
				return true
			}
			life := content.Life{Kind: "m", TemplateID: stats.ID, X: altar.def.X, Y: -212, Foothold: 33, RespawnSeconds: -1}
			battle.forms[phase] = &gameplayMob{objectID: objectID, life: life, origin: life, stats: stats, hp: stats.HP, fake: phase == 0, oneTime: true}
		}
	}
	battle.lastHit = now
	altar.state++
	updates := roomBroadcast(room, protocol.ReactorState(altar.objectID, altar.state, altar.def.X, altar.def.Y), -1)
	if lastHit {
		updates = append(updates, roomBroadcast(room, protocol.FieldMusic("Bgm09/TimeAttack"), -1)...)
		updates = append(updates, roomBroadcast(room, protocol.PortalTrainingMessage("克雷賽爾出現了。"), -1)...)
		updates = append(updates, spawnKrexelLocked(room, battle.forms[0])...)
	}
	g.mu.Unlock()
	deliver(updates)
	if lastHit {
		time.AfterFunc(time.Second, func() {
			g.mu.Lock()
			if g.rooms[key] != room || room.krexel != battle || battle.stage != 0 {
				g.mu.Unlock()
				return
			}
			opener := battle.forms[0]
			delete(room.mobs, opener.objectID)
			battle.stage = 1
			updates := roomBroadcast(room, protocol.RemoveMonster(opener.objectID), -1)
			updates = append(updates, spawnKrexelLocked(room, battle.forms[1])...)
			g.mu.Unlock()
			deliver(updates)
		})
	}
	return true
}

func (s *Server) krexelDefeated(key mapKey, mobID int) {
	if mobID != 9420521 && mobID != 9420522 {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.krexel == nil {
		g.mu.Unlock()
		return
	}
	battle := room.krexel
	if battle.stage < 1 || battle.stage >= len(battle.forms) {
		g.mu.Unlock()
		return
	}
	defeated := battle.forms[battle.stage]
	if defeated.stats.ID != mobID || defeated.hp != 0 {
		g.mu.Unlock()
		return
	}
	battle.stage++
	var updates []roomDelivery
	if battle.stage < len(battle.forms) {
		updates = spawnKrexelLocked(room, battle.forms[battle.stage])
	}
	g.mu.Unlock()
	deliver(updates)
}

func spawnKrexelLocked(room *gameplayRoom, mob *gameplayMob) []roomDelivery {
	room.mobs[mob.objectID] = mob
	now := time.Now()
	updates := roomBroadcast(room, mob.spawnPacket(false, now), -1)
	if !mob.fake {
		for id, member := range room.members {
			mob.controller = id
			updates = append(updates, roomDelivery{member.sink, mob.spawnPacket(true, now)})
			break
		}
	}
	return updates
}

func (room *gameplayRoom) resetKrexel() {
	for _, form := range room.krexel.forms {
		if form != nil {
			delete(room.mobs, form.objectID)
		}
	}
	room.krexel.altar.state = 0
	room.krexel = nil
}
