package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type zakumOfferingRepository interface {
	ConsumeZakumEye(context.Context, domain.CharacterSession, int32) error
}

// A room and its altar are reset together. Its phase is protected by
// gameplay.mu: 0 waiting, 1 arms, 2-4 body forms, 5 defeated.
type zakumField struct {
	altar      content.ZakumAltar
	objectID   int32
	projection int32
	phase      int
	offering   bool
}

func (z *zakumField) enterPacket() []byte {
	return protocol.ReactorEnter(z.objectID, z.altar.ReactorID, min(z.phase, 1), z.altar.X, z.altar.Y, 0)
}

func qualifiedForZakum(c domain.Character) bool {
	// This historical server-only ID does not fit the native quest journal's
	// u16 and is absent from v148 WZ. Retain it as a private script record.
	if c.QuestRecords[content.ZakumQuest] == "2" {
		for _, item := range c.Items {
			if item.TemplateID == content.ZakumEye && item.Quantity > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Server) enterZakumLobby(ctx context.Context, session domain.CharacterSession, live *liveCharacter, sink packetSink) error {
	if !qualifiedForZakum(session.Character) {
		_ = sink.WritePacket(protocol.PortalTrainingMessage("須完成前置任務並帶著火焰之眼，才能前往候場。"))
		return sink.WritePacket(protocol.EnableActions())
	}
	dest, err := s.gameplay.catalog.Map(session.Character.MapID + 100)
	if err != nil {
		return err
	}
	entry, ok := dest.Portal("west00")
	if !ok {
		return domain.ErrNotFound
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	return s.warpGameplay(ctx, session.ChannelID, live, sink, dest.ID, entry)
}

func (g *gameplaySpace) prepareZakumMob(stats content.Mob, fake bool) *gameplayMob {
	oid := g.alloc()
	if oid == 0 {
		return nil
	}
	life := content.Life{Kind: "m", TemplateID: stats.ID, X: -10, Y: -215, Foothold: 70, RespawnSeconds: -1}
	return &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, fake: fake}
}

// Call only for a genuine committed native item drop, never a script-owned
// object. The altar, roster and item are checked again under their own locks.
func (s *Server) offerZakumEye(ctx context.Context, session domain.CharacterSession, drop domain.ItemDrop) {
	altar, ok := content.ZakumAltarByMap(drop.MapID)
	if !ok || s.events == nil || s.gameplay == nil || drop.TemplateID != content.ZakumEye ||
		drop.Quantity != 1 || drop.OwnerID != session.CharacterID || session.Character.MapID != drop.MapID ||
		session.ChannelID != drop.ChannelID || !altar.CanOffer(drop.X, drop.Y) {
		return
	}
	repo, ok := s.repo.(zakumOfferingRepository)
	if !ok {
		return
	}
	g := s.gameplay
	// Verify all encounter templates are mounted before taking any item.
	stats := make([]content.Mob, 11)
	for i := range stats {
		m, err := g.catalog.Mob(altar.BaseMob + i)
		if err != nil {
			slog.Warn("zakum_data_missing", "map", drop.MapID, "mob", altar.BaseMob+i, "error", err)
			return
		}
		stats[i] = m
	}
	key := mapKey{channel: drop.ChannelID, mapID: drop.MapID}
	raid := s.eventOf(session.CharacterID)
	if raid == nil {
		return
	}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.zakum == nil || room.zakum.phase != 0 || room.zakum.offering ||
		!s.expeditionCombatAllowedLocked(key, session.CharacterID) {
		g.mu.Unlock()
		return
	}
	parts := []*gameplayMob{g.prepareZakumMob(stats[0], true)}
	for i := 3; i <= 10; i++ {
		parts = append(parts, g.prepareZakumMob(stats[i], false))
	}
	for _, m := range parts {
		if m == nil {
			g.mu.Unlock()
			return
		}
	}
	room.zakum.offering = true
	g.mu.Unlock()
	step, stop := context.WithTimeout(ctx, 3*time.Second)
	err := repo.ConsumeZakumEye(step, session, drop.ObjectID)
	stop()
	g.mu.Lock()
	room.zakum.offering = false
	active := g.rooms[key] == room && s.eventOf(session.CharacterID) == raid
	if err != nil || !active {
		g.mu.Unlock()
		if err == nil {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		return
	}
	room.zakum.phase, room.zakum.projection = 1, parts[0].objectID
	reactor := protocol.ReactorState(room.zakum.objectID, 1, altar.X, altar.Y)
	var packets [][]byte
	for _, m := range parts {
		room.mobs[m.objectID] = m
		packets = append(packets, m.spawnPacket(false, time.Now()))
	}
	g.mu.Unlock()
	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	s.broadcastInventoryPacket(key.channel, key.mapID, reactor)
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.FieldMusic("Bgm06/FinalFight"))
	for _, p := range packets {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("火焰之眼已喚醒炎魔。先擊倒八隻手臂！"))
}

func (s *Server) zakumPartDefeated(key mapKey, actor int64, mob int) {
	altar, ok := content.ZakumAltarByMap(key.mapID)
	if !ok || s.events == nil || s.gameplay == nil || mob < altar.BaseMob || mob > altar.BaseMob+10 {
		return
	}
	g := s.gameplay
	nextID := altar.BaseMob
	if mob >= altar.BaseMob && mob < altar.BaseMob+2 {
		nextID = mob + 1
	}
	next, err := g.catalog.Mob(nextID)
	if err != nil {
		return
	}
	packets, clear := func() ([][]byte, bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		room := g.rooms[key]
		if room == nil || room.zakum == nil || !s.expeditionCombatAllowedLocked(key, actor) {
			return nil, false
		}
		z := room.zakum
		var packets [][]byte
		switch {
		case z.phase == 1 && mob >= altar.BaseMob+3:
			for _, m := range room.mobs {
				if !m.fake && m.hp > 0 && m.life.TemplateID >= altar.BaseMob+3 && m.life.TemplateID <= altar.BaseMob+10 {
					return nil, false
				}
			}
			delete(room.mobs, z.projection)
			packets = append(packets, protocol.RemoveMonster(z.projection))
		case z.phase == 2 && mob == altar.BaseMob, z.phase == 3 && mob == altar.BaseMob+1:
			// The committed defeat of this form releases the next one.
		case z.phase == 4 && mob == altar.BaseMob+2:
			s.events.mu.Lock()
			inst := s.events.byMember[actor]
			cleared := inst != nil && inst.channel == key.channel && !inst.ended && len(inst.members) >= inst.definition.Keep
			if cleared {
				inst.cleared = true
				z.phase = 5
			}
			s.events.mu.Unlock()
			return nil, cleared
		default:
			return nil, false
		}
		m := g.prepareZakumMob(next, false)
		if m == nil {
			return nil, false
		}
		z.phase++
		room.mobs[m.objectID] = m
		packets = append(packets, m.spawnPacket(false, time.Now()))
		return packets, false
	}()
	for _, p := range packets {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	if clear {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗炎魔！請拾取戰利品，再找場內嚮導離開。"))
	}
}
