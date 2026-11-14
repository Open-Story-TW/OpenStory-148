package world

import "maple-go-server/internal/protocol"

// Evan dragons (Java MapleDragon) are presentation-only: they ride the map
// AOI with their owner. The owner sees its own dragon on map entry, peers get
// it right after the owner's SpawnPlayer, and MOVE_DRAGON is relayed to the
// owner's visible peers.

func (p *mapMember) dragonPacket() []byte {
	if !protocol.HasDragon(p.look.Job) {
		return nil
	}
	x, y := p.x, p.y
	if p.dragonMoved {
		x, y = p.dragonX, p.dragonY
	}
	return protocol.SpawnDragon(p.id, x, y, 4, p.look.Job)
}

// writeMemberSpawn shows source to a viewer: the player, then its dragon.
func writeMemberSpawn(viewer packetSink, source *mapMember) {
	_ = viewer.WritePacket(protocol.SpawnPlayer(source.look, 4))
	if p := source.dragonPacket(); p != nil {
		_ = viewer.WritePacket(p)
	}
}

// MoveDragon records the dragon position and relays the movement.
func (m *mapSpace) MoveDragon(channel, mapID int, characterID int64, mv protocol.DragonMoveInput, expected ...packetSink) {
	m.publication.Lock()
	defer m.publication.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	self := m.rooms[mapKey{channel: channel, mapID: mapID}][characterID]
	if self == nil || !protocol.HasDragon(self.look.Job) || len(expected) > 0 && !sameDropSink(self.sink, expected[0]) {
		return
	}
	if mv.Changed {
		self.dragonX, self.dragonY, self.dragonMoved = mv.X, mv.Y, true
	}
	packet := protocol.MoveDragon(characterID, mv.StartX, mv.StartY, mv.Fragments)
	if packet == nil {
		return
	}
	for peerID := range self.visible {
		if peer := m.rooms[mapKey{channel: channel, mapID: mapID}][peerID]; peer != nil {
			_ = peer.sink.WritePacket(packet)
		}
	}
	for _, peer := range m.rooms[mapKey{channel: channel, mapID: mapID}] {
		if _, ok := peer.watching[self.id]; ok {
			_ = peer.sink.WritePacket(packet)
		}
	}
}

// RefreshDragon applies a job change (Java changeJob): the old dragon is
// removed and an Evan job gets a new one at the owner's position.
func (m *mapSpace) RefreshDragon(channel, mapID int, characterID int64, job int) {
	m.publication.Lock()
	defer m.publication.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.rooms[mapKey{channel: channel, mapID: mapID}]
	if self := room[characterID]; self != nil {
		refreshDragonLocked(room, self, job)
	}
}

// refreshDragonLocked runs under mu before self.look takes the new job.
func refreshDragonLocked(room map[int64]*mapMember, self *mapMember, job int) {
	if self.look.Job == job {
		return
	}
	characterID := self.id
	had := protocol.HasDragon(self.look.Job)
	self.look.Job = job
	self.dragonMoved = false
	var out [][]byte
	if had {
		out = append(out, protocol.RemoveDragon(characterID))
	}
	if p := self.dragonPacket(); p != nil {
		out = append(out, p)
	}
	viewers := []packetSink{self.sink}
	for peerID := range self.visible {
		if peer := room[peerID]; peer != nil {
			viewers = append(viewers, peer.sink)
		}
	}
	for _, peer := range room {
		if _, ok := peer.watching[self.id]; ok {
			viewers = append(viewers, peer.sink)
		}
	}
	for _, v := range viewers {
		for _, p := range out {
			_ = v.WritePacket(p)
		}
	}
}

func (s *Server) dragonMove(channel int, live *liveCharacter, sink packetSink, body []byte) {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	if summonMoveDiag.Add(1) <= 20 {
		summonDiag("dragon_move_in", c.ID, body)
	}
	mv, err := protocol.ReadDragonMove(protocol.NewReader(body))
	if err != nil {
		summonDiag("dragon_move_rejected", c.ID, body)
		return
	}
	if s.maps != nil {
		s.maps.MoveDragon(channel, c.MapID, c.ID, mv, sink)
	}
}

func (s *Server) refreshDragon(channel, mapID int, characterID int64, job int) {
	if s.maps == nil {
		return
	}
	s.maps.RefreshDragon(channel, mapID, characterID, job)
}
