package world

import (
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

type carnivalDelivery struct {
	visit   *sceneVisit
	packets [][]byte
}

func carnivalBroadcastLocked(room *gameplayRoom, packets ...[]byte) []carnivalDelivery {
	deliveries := make([]carnivalDelivery, 0, len(room.members))
	for _, in := range room.members {
		deliveries = append(deliveries, carnivalDelivery{in.visit, packets})
	}
	return deliveries
}

func (s *Server) publishCarnivalDeliveries(deliveries []carnivalDelivery) {
	for _, d := range deliveries {
		_ = s.writeScenePackets(d.visit, d.packets...)
	}
}

func (g *gameplaySpace) carnivalCanAttackLocked(room *gameplayRoom, id int64, now time.Time) bool {
	if room.carnival == nil {
		return true
	}
	m := room.carnival
	p := g.carnival.entrants[id]
	return p != nil && p.match == m && !p.dead && m.ready && !m.canceled &&
		m.changesAt.IsZero() && now.Before(m.endsAt)
}

func (g *gameplaySpace) carnivalScoreLocked(room *gameplayRoom, t *combatTicket, points int) {
	if room == nil || room.carnival == nil || points <= 0 {
		return
	}
	m := room.carnival
	id := t.actorSession.CharacterID
	p := g.carnival.entrants[id]
	if p == nil || p.match != m || m.canceled || !m.ready || !m.changesAt.IsZero() {
		return
	}
	entry := room.members[id]
	if entry == nil || entry.live != t.actor || !sameDropSink(entry.sink, t.sink) {
		return
	}
	p.available += points
	p.earned += points
	m.totals[p.team] += points
	t.carnivalMatch = m
}

func (s *Server) publishCarnivalScores(t *combatTicket) {
	if t.carnivalMatch == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[t.key]
	if room == nil || room.carnival != t.carnivalMatch {
		g.mu.Unlock()
		return
	}
	p := g.carnival.entrants[t.actorSession.CharacterID]
	deliveries := carnivalBroadcastLocked(room, protocol.CarnivalScore(t.carnivalMatch.totals[carnivalRed], t.carnivalMatch.totals[carnivalBlue]))
	entry := room.members[t.actorSession.CharacterID]
	if p != nil && p.match == t.carnivalMatch && entry != nil && entry.live == t.actor {
		deliveries = append(deliveries, carnivalDelivery{entry.visit, [][]byte{protocol.CarnivalCP(p.available, p.earned)}})
	}
	g.mu.Unlock()
	s.publishCarnivalDeliveries(deliveries)
}

func (s *Server) handleCarnivalAction(channel int, live *liveCharacter, sink packetSink, body []byte) error {
	action, err := protocol.ReadCarnivalAction(body)
	snapshot := partySession(live)
	visit := s.sceneFor(live, sink)
	if err != nil || visit == nil || snapshot.Character.HP <= 0 || content.CarnivalArenaMode(snapshot.Character.MapID) == 0 {
		return nil
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: snapshot.Character.MapID}]
	if room == nil || room.carnival == nil || !g.carnivalCanAttackLocked(room, snapshot.CharacterID, time.Now()) {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(1))
	}
	entry := room.members[snapshot.CharacterID]
	p := g.carnival.entrants[snapshot.CharacterID]
	if entry == nil || entry.visit != visit || p == nil {
		g.mu.Unlock()
		return nil
	}
	if action != 0 {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(1))
	}
	m := room.carnival
	selection := rand.IntN(len(m.arena.Summons))
	option := m.arena.Summons[selection]
	cost := option.Cost
	if m.legacyID != "" {
		// Piggy2's four-button Carnival handler uses the 100 CP shown by
		// the available v148 UI even on the retained Mode II roster.
		cost = 100
	}
	if p.available < cost {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(1))
	}
	mob, err := g.catalog.Mob(option.MobID)
	if err != nil || mob.HP <= 0 || mob.CarnivalPoints <= 0 {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(2))
	}
	busy := make(map[int]bool)
	for _, existing := range room.mobs {
		if existing.carnivalSummoned && (existing.hp > 0 || !existing.respawnAt.IsZero()) {
			busy[existing.origin.Index] = true
		}
	}
	if len(busy)+option.Count > m.arena.MaxSummoned {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(2))
	}
	spawns := make([]*gameplayMob, 0, option.Count)
	for i, point := range m.arena.SpawnPoints {
		if busy[i] {
			continue
		}
		point.Index, point.TemplateID = i, option.MobID
		point.RespawnSeconds = option.RespawnSeconds
		oid := g.alloc()
		if oid == 0 {
			break
		}
		m := &gameplayMob{objectID: oid, life: point, origin: point, stats: mob, hp: mob.HP,
			controller: snapshot.CharacterID, oneTime: m.legacyID == "", carnivalSummoned: true}
		spawns = append(spawns, m)
		if len(spawns) == option.Count {
			break
		}
	}
	if len(spawns) != option.Count {
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.CarnivalActionError(2))
	}
	now := time.Now()
	p.available -= cost
	packets := [][]byte{protocol.CarnivalActionAccepted(action, byte(selection), snapshot.Character.Name)}
	var controls [][]byte
	for _, summoned := range spawns {
		room.mobs[summoned.objectID] = summoned
		packets = append(packets, summoned.spawnPacket(false, now))
		controls = append(controls, summoned.spawnPacket(true, now))
	}
	deliveries := carnivalBroadcastLocked(room, packets...)
	deliveries = append(deliveries, carnivalDelivery{visit, append(controls, protocol.CarnivalCP(p.available, p.earned))})
	g.mu.Unlock()
	s.publishCarnivalDeliveries(deliveries)
	return nil
}
