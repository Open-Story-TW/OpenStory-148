package world

import (
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// DefaultAOIRadius is the initial visibility distance in map pixels.
// Full-map broadcast is avoided; OPTIMIZATIONS prefers spatial AOI.
const DefaultAOIRadius = 800

type packetSink interface {
	WritePacket([]byte) error
}

type mapKey struct {
	channel int
	mapID   int
}

type mapMember struct {
	id      int64
	x, y    int
	look    domain.Character
	sink    packetSink
	visible map[int64]struct{}
	// Evan dragon position from MOVE_DRAGON; the owner position until moved.
	dragonX, dragonY int
	dragonMoved      bool
	// hidden members (GM hide) are in nobody's visible set, so every
	// broadcast skips them; watching are the peers spawned on a hidden
	// member's own client, which still sees them move.
	hidden   bool
	watching map[int64]struct{}
}

// gmHidden reports whether look carries an active GM_HIDE buff.
func gmHidden(look domain.Character) bool {
	now := time.Now()
	for _, b := range look.SkillBuffs {
		if b.Stat == "GM_HIDE" && b.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// link records that peer is within range of self and spawns whichever side
// may see the other. Called under mu.
func (m *mapMember) link(peer *mapMember, spawned *[]mapSpawnPair) {
	switch {
	case !m.hidden && !peer.hidden:
		m.visible[peer.id] = struct{}{}
		peer.visible[m.id] = struct{}{}
		writeMemberSpawn(m.sink, peer)
		writeMemberSpawn(peer.sink, m)
		*spawned = append(*spawned, mapSpawnPair{peer, m}, mapSpawnPair{m, peer})
	case m.hidden && !peer.hidden:
		m.watching[peer.id] = struct{}{}
		writeMemberSpawn(m.sink, peer)
		*spawned = append(*spawned, mapSpawnPair{peer, m})
	case !m.hidden && peer.hidden:
		peer.watching[m.id] = struct{}{}
		writeMemberSpawn(peer.sink, m)
		*spawned = append(*spawned, mapSpawnPair{m, peer})
	}
}

// unlink removes both directions of a link. Called under mu.
func (m *mapMember) unlink(peer *mapMember) {
	if _, ok := m.visible[peer.id]; ok {
		delete(m.visible, peer.id)
		delete(peer.visible, m.id)
		_ = m.sink.WritePacket(protocol.RemovePlayer(peer.id))
		_ = peer.sink.WritePacket(protocol.RemovePlayer(m.id))
	}
	if _, ok := m.watching[peer.id]; ok {
		delete(m.watching, peer.id)
		_ = m.sink.WritePacket(protocol.RemovePlayer(peer.id))
	}
	if _, ok := peer.watching[m.id]; ok {
		delete(peer.watching, m.id)
		_ = peer.sink.WritePacket(protocol.RemovePlayer(m.id))
	}
}

// linked reports whether m and peer currently have any link.
func (m *mapMember) linked(peer *mapMember) bool {
	_, a := m.visible[peer.id]
	_, b := m.watching[peer.id]
	_, c := peer.watching[m.id]
	return a || b || c
}

// mapSpace tracks players per channel+map and applies distance AOI.
type mapSpace struct {
	publication sync.Mutex // Spawn/remove and dependent remote-state delivery order.
	mu          sync.Mutex
	radius      int
	rooms       map[mapKey]map[int64]*mapMember
	spawnState  func(mapKey, *mapMember, *mapMember) // Called under publication, outside mu.
}

type mapSpawnPair struct{ source, target *mapMember }

func newMapSpace(radius int) *mapSpace {
	if radius < 1 {
		radius = DefaultAOIRadius
	}
	return &mapSpace{radius: radius, rooms: map[mapKey]map[int64]*mapMember{}}
}

func (m *mapSpace) Enter(channel, mapID int, look domain.Character, sink packetSink) {
	if channel < 1 || mapID < 0 || look.ID <= 0 || sink == nil {
		return
	}
	key := mapKey{channel: channel, mapID: mapID}
	m.publication.Lock()
	defer m.publication.Unlock()
	var spawned []mapSpawnPair
	self := &mapMember{
		id: look.ID, x: look.X, y: look.Y, look: look, sink: sink,
		visible: map[int64]struct{}{}, watching: map[int64]struct{}{}, hidden: gmHidden(look),
	}
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if m.spawnState != nil {
			for _, pair := range spawned {
				m.spawnState(key, pair.source, pair.target)
			}
		}
	}()
	room := m.rooms[key]
	if room == nil {
		room = map[int64]*mapMember{}
		m.rooms[key] = room
	}
	// Replace stale entry if any.
	delete(room, self.id)
	room[self.id] = self
	if p := self.dragonPacket(); p != nil {
		_ = self.sink.WritePacket(p)
	}
	for _, peer := range room {
		if peer.id == self.id {
			continue
		}
		if !within(self.x, self.y, peer.x, peer.y, m.radius) {
			continue
		}
		self.link(peer, &spawned)
	}
}

func (m *mapSpace) Leave(channel, mapID int, characterID int64, expected ...packetSink) {
	m.publication.Lock()
	defer m.publication.Unlock()
	key := mapKey{channel: channel, mapID: mapID}
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.rooms[key]
	if room == nil {
		return
	}
	self := room[characterID]
	if self == nil {
		return
	}
	if len(expected) > 0 && !sameDropSink(self.sink, expected[0]) {
		return
	}
	for peerID := range self.visible {
		if peer := room[peerID]; peer != nil {
			delete(peer.visible, characterID)
			_ = peer.sink.WritePacket(protocol.RemovePlayer(characterID))
		}
	}
	for _, peer := range room {
		if _, ok := peer.watching[characterID]; ok {
			delete(peer.watching, characterID)
			_ = peer.sink.WritePacket(protocol.RemovePlayer(characterID))
		}
	}
	delete(room, characterID)
	if len(room) == 0 {
		delete(m.rooms, key)
	}
}

// Move updates position, adjusts AOI spawn/despawn, and relays movement to visible peers.
func (m *mapSpace) Move(channel, mapID int, characterID int64, look domain.Character, startX, startY int16, fragments []byte, expected ...packetSink) {
	key := mapKey{channel: channel, mapID: mapID}
	m.publication.Lock()
	defer m.publication.Unlock()
	var spawned []mapSpawnPair
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if m.spawnState != nil {
			for _, pair := range spawned {
				m.spawnState(key, pair.source, pair.target)
			}
		}
	}()
	room := m.rooms[key]
	if room == nil {
		return
	}
	self := room[characterID]
	if self == nil {
		return
	}
	// A delayed packet from the previous socket must not move the member
	// installed by a replacement login, even in the same channel and map.
	if len(expected) > 0 && !sameDropSink(self.sink, expected[0]) {
		return
	}
	self.x = look.X
	self.y = look.Y
	self.look = look
	for _, peer := range room {
		if peer.id == self.id {
			continue
		}
		in := within(self.x, self.y, peer.x, peer.y, m.radius)
		switch {
		case in && !self.linked(peer):
			self.link(peer, &spawned) // Newly in range.
		case !in && self.linked(peer):
			self.unlink(peer) // Lost range.
		}
	}
	packet := protocol.MovePlayer(characterID, startX, startY, fragments)
	if packet == nil {
		return
	}
	for peerID := range self.visible {
		if peer := room[peerID]; peer != nil {
			_ = peer.sink.WritePacket(packet)
		}
	}
	for _, peer := range room {
		if _, ok := peer.watching[self.id]; ok {
			_ = peer.sink.WritePacket(packet)
		}
	}
}

// SetHidden switches GM hide for a member: hiding despawns it for every
// peer (it keeps watching them); unhiding spawns it again for peers in
// range.
func (m *mapSpace) SetHidden(channel, mapID int, characterID int64, hidden bool) {
	key := mapKey{channel: channel, mapID: mapID}
	m.publication.Lock()
	defer m.publication.Unlock()
	var spawned []mapSpawnPair
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if m.spawnState != nil {
			for _, pair := range spawned {
				m.spawnState(key, pair.source, pair.target)
			}
		}
	}()
	self := m.rooms[key][characterID]
	if self == nil || self.hidden == hidden {
		return
	}
	room := m.rooms[key]
	var near []*mapMember
	for _, peer := range room {
		if peer.id != self.id && self.linked(peer) {
			near = append(near, peer)
		}
	}
	for _, peer := range near {
		self.unlink(peer)
	}
	self.hidden = hidden
	for _, peer := range near {
		self.link(peer, &spawned)
	}
}

func within(ax, ay, bx, by, radius int) bool {
	dx := ax - bx
	dy := ay - by
	return dx*dx+dy*dy <= radius*radius
}

// VisibleCount is test/helper access to one member's AOI set size.
func (m *mapSpace) VisibleCount(channel, mapID int, characterID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.rooms[mapKey{channel: channel, mapID: mapID}]
	if room == nil || room[characterID] == nil {
		return 0
	}
	return len(room[characterID].visible)
}
