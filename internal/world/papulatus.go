package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const (
	papulatusReactor = 2201004
	papulatusCrystal = 8500000
	papulatusClock   = 8500001
	papulatusBody    = 8500002
)

type papulatusOfferingRepository interface {
	ConsumePapulatusFragment(context.Context, domain.CharacterSession, int32) error
}

// The scene lock covers the offering and current form. The altar does not
// accept another fragment or respawn while this attempt is still running.
type papulatusField struct {
	altar    *roomReactor
	offering bool
	form     int
}

func (g *gameplaySpace) addPapulatusReactors(room *gameplayRoom, mapID int) error {
	var reactors []content.Reactor
	switch mapID {
	case content.PapulatusLobby:
		reactors = []content.Reactor{
			{TemplateID: 2208001, X: -289, Y: -772},
			{TemplateID: 2208002, X: -294, Y: -848},
			{TemplateID: 2208003, X: -299, Y: -735},
		}
	case content.PapulatusArena:
		reactors = []content.Reactor{{TemplateID: papulatusReactor, X: -181, Y: -439, Respawn: -1}}
	default:
		return nil
	}
	if room.reactors == nil {
		room.reactors = make(map[int32]*roomReactor, len(reactors))
	}
	for _, def := range reactors {
		oid := g.alloc()
		if oid == 0 {
			return errors.New("unable to allocate a Clocktower reactor")
		}
		r := &roomReactor{objectID: oid, def: def}
		room.reactors[oid] = r
		if def.TemplateID == papulatusReactor {
			room.papulatus = &papulatusField{altar: r}
		}
	}
	return nil
}

func papulatusGatePackets(room *gameplayRoom, state int) [][]byte {
	if room == nil {
		return nil
	}
	var packets [][]byte
	for _, r := range room.reactors {
		if r.def.TemplateID < 2208001 || r.def.TemplateID > 2208003 || r.state == state {
			continue
		}
		r.state = state
		packets = append(packets, protocol.ReactorState(r.objectID, state, r.def.X, r.def.Y))
	}
	return packets
}

func (s *Server) setPapulatusGate(channel, state int) {
	g := s.gameplay
	g.mu.Lock()
	packets := papulatusGatePackets(g.rooms[mapKey{channel: channel, mapID: content.PapulatusLobby}], state)
	g.mu.Unlock()
	for _, packet := range packets {
		s.broadcastInventoryPacket(channel, content.PapulatusLobby, packet)
	}
}

func (g *gameplaySpace) newPapulatusMob(stats content.Mob) *gameplayMob {
	oid := g.alloc()
	if oid == 0 {
		return nil
	}
	life := content.Life{Kind: "m", TemplateID: stats.ID, X: -410, Y: -386, Foothold: 20, RespawnSeconds: -1}
	return &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
}

func (s *Server) offerPapulatusFragment(ctx context.Context, session domain.CharacterSession, drop domain.ItemDrop) {
	if drop.MapID != content.PapulatusArena || session.Character.MapID != drop.MapID || drop.ChannelID != session.ChannelID ||
		drop.TemplateID != content.PapulatusFragment || drop.Quantity != 1 || drop.OwnerID != session.CharacterID ||
		drop.X < content.PapulatusOfferLeft || drop.X > content.PapulatusOfferRight ||
		drop.Y < content.PapulatusOfferTop || drop.Y > content.PapulatusOfferBottom || s.events == nil || s.gameplay == nil {
		return
	}
	repo, ok := s.repo.(papulatusOfferingRepository)
	if !ok {
		return
	}
	g := s.gameplay
	crystal, err := g.catalog.Mob(papulatusCrystal)
	if err != nil {
		return
	}
	key := mapKey{channel: drop.ChannelID, mapID: drop.MapID}
	attempt := s.eventOf(session.CharacterID)
	if attempt == nil {
		return
	}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.papulatus == nil || room.papulatus.form != 0 || room.papulatus.offering ||
		!s.expeditionCombatAllowedLocked(key, session.CharacterID) {
		g.mu.Unlock()
		return
	}
	mob := g.newPapulatusMob(crystal)
	if mob == nil {
		g.mu.Unlock()
		return
	}
	room.papulatus.offering = true
	g.mu.Unlock()

	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	err = repo.ConsumePapulatusFragment(step, session, drop.ObjectID)
	cancel()
	g.mu.Lock()
	room.papulatus.offering = false
	if err != nil || g.rooms[key] != room || s.eventOf(session.CharacterID) != attempt {
		g.mu.Unlock()
		if err == nil {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		return
	}
	room.papulatus.form = papulatusCrystal
	altar := room.papulatus.altar
	altar.state = 1
	room.mobs[mob.objectID] = mob
	gates := papulatusGatePackets(g.rooms[mapKey{channel: key.channel, mapID: content.PapulatusLobby}], 1)
	g.mu.Unlock()

	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.ReactorState(altar.objectID, 1, altar.def.X, altar.def.Y))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.FieldMusic("Bgm09/TimeAttack"))
	s.broadcastInventoryPacket(key.channel, key.mapID, mob.spawnPacket(false, time.Now()))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("龜裂的碎片填補了裂縫，神奇水晶出現了。"))
	for _, packet := range gates {
		s.broadcastInventoryPacket(key.channel, content.PapulatusLobby, packet)
	}
}

func (s *Server) papulatusPartDefeated(key mapKey, actor int64, mobID int) {
	if s.events == nil || s.gameplay == nil || mobID < papulatusCrystal || mobID > papulatusBody {
		return
	}
	g := s.gameplay
	var next content.Mob
	if mobID < papulatusBody {
		stats, err := g.catalog.Mob(mobID + 1)
		if err != nil {
			return
		}
		next = stats
	}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.papulatus == nil || room.papulatus.form != mobID || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	if mobID < papulatusBody {
		mob := g.newPapulatusMob(next)
		if mob == nil {
			g.mu.Unlock()
			return
		}
		room.mobs[mob.objectID] = mob
		room.papulatus.form = next.ID
		g.mu.Unlock()
		s.broadcastInventoryPacket(key.channel, key.mapID, mob.spawnPacket(false, time.Now()))
		return
	}
	s.events.mu.Lock()
	attempt := s.events.byMember[actor]
	won := attempt != nil && !attempt.ended && !attempt.cleared
	if won {
		attempt.cleared = true
		room.papulatus.form = -1
	}
	s.events.mu.Unlock()
	g.mu.Unlock()
	if won {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗拉圖斯！請拾取獎勵，再找場內出口。"))
	}
}
