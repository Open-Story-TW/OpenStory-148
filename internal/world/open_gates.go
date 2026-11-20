package world

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
)

// openGate is one Mechanic Open Gate (35101005). An owner keeps at most two
// gates (first and second) on the map they were cast on; the pair moves the
// owner and their party between them (COpenGatePool, 0x649339/0x648B2B).
type openGate struct {
	ownerID         int64
	ownerSession    string
	ownerGeneration int64
	partyID         int64
	mapID           int
	x, y            int
	second          bool
	createdAt       time.Time
	expiresAt       time.Time
}

type gateKey struct {
	channel int
	owner   int64
	second  bool
}

func (gate *openGate) created(animated bool) []byte {
	return protocol.OpenGateCreated(gate.ownerID, int16(gate.x), int16(gate.y), gate.second, animated, gate.partyID)
}

func (gate *openGate) removed() []byte {
	return protocol.OpenGateRemoved(gate.ownerID, gate.second, true)
}

// usableBy is the client's own check in 0x648B2B: the owner, or a member of
// the party the gate was cast in.
func (gate *openGate) usableBy(id, party int64) bool {
	return id == gate.ownerID || gate.partyID != 0 && party == gate.partyID
}

// spawnOpenGate places a gate at the caster's position. The first cast makes
// the first gate, the next one the second; with both up, a cast replaces the
// older one in its slot (Java MechDoor). Gates left on another map go first.
func (s *Server) spawnOpenGate(channel int, live *liveCharacter, skillID, level int, spec *combat.AttackSpec, now time.Time) error {
	_, d, err := fieldSkillEffect(skillID, level, spec)
	if err != nil {
		return err
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	party := s.partyOf(c.ID)
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return fmt.Errorf("open gate owner not in field")
	}
	if g.gates == nil {
		g.gates = map[gateKey]*openGate{}
	}
	var out []roomDelivery
	for _, second := range []bool{false, true} {
		if old := g.gates[gateKey{channel, c.ID, second}]; old != nil && old.mapID != c.MapID {
			out = append(out, g.removeGateLocked(channel, old)...)
		}
	}
	first, second := g.gates[gateKey{channel, c.ID, false}], g.gates[gateKey{channel, c.ID, true}]
	slot := false
	switch {
	case first == nil:
	case second == nil:
		slot = true
	default:
		replaced := first
		if second.createdAt.Before(first.createdAt) {
			replaced = second
		}
		slot = replaced.second
		out = append(out, g.removeGateLocked(channel, replaced)...)
	}
	gate := &openGate{ownerID: c.ID, ownerSession: session.SessionID, ownerGeneration: session.Generation, partyID: party,
		mapID: c.MapID, x: c.X, y: c.Y, second: slot, createdAt: now, expiresAt: now.Add(d)}
	g.gates[gateKey{channel, c.ID, slot}] = gate
	out = append(out, roomBroadcast(room, gate.created(true), -1)...)
	g.mu.Unlock()
	deliver(out)
	return nil
}

func (g *gameplaySpace) removeGateLocked(channel int, gate *openGate) []roomDelivery {
	delete(g.gates, gateKey{channel, gate.ownerID, gate.second})
	if room := g.rooms[mapKey{channel: channel, mapID: gate.mapID}]; room != nil {
		return roomBroadcast(room, gate.removed(), -1)
	}
	return nil
}

// gateEnterPacketsLocked draws the gates of a map for a joining member, each
// owner's first gate before the second so the second links to it.
func (g *gameplaySpace) gateEnterPacketsLocked(channel, mapID int) [][]byte {
	var gates []*openGate
	for k, gate := range g.gates {
		if k.channel == channel && gate.mapID == mapID {
			gates = append(gates, gate)
		}
	}
	sort.Slice(gates, func(i, j int) bool {
		if gates[i].ownerID != gates[j].ownerID {
			return gates[i].ownerID < gates[j].ownerID
		}
		return !gates[i].second && gates[j].second
	})
	out := make([][]byte, 0, len(gates))
	for _, gate := range gates {
		out = append(out, gate.created(false))
	}
	return out
}

// tickGates removes gates that expired or whose owner left the gate's map
// (map change, channel change or logout).
func (g *gameplaySpace) tickGates(now time.Time) {
	var out []roomDelivery
	g.mu.Lock()
	for k, gate := range g.gates {
		room := g.rooms[mapKey{channel: k.channel, mapID: gate.mapID}]
		var owner *gameplayMember
		if room != nil {
			owner = room.members[gate.ownerID]
		}
		if !now.Before(gate.expiresAt) || owner == nil || owner.sessionID != gate.ownerSession || owner.generation != gate.ownerGeneration {
			out = append(out, g.removeGateLocked(k.channel, gate)...)
		}
	}
	g.mu.Unlock()
	deliver(out)
}

// openGateTarget finds the gate a user at (x, y) on mapID stands at and its
// pair, both still up, when the user may use the owner's gates.
func (g *gameplaySpace) openGateTarget(channel int, owner int64, x, y int16, mapID, charX, charY int, id, party int64, now time.Time) *openGate {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, second := range []bool{false, true} {
		gate := g.gates[gateKey{channel, owner, second}]
		other := g.gates[gateKey{channel, owner, !second}]
		if gate == nil || other == nil || gate.mapID != mapID || other.mapID != mapID || !now.Before(gate.expiresAt) || !now.Before(other.expiresAt) {
			continue
		}
		// 0x648B2B tests the user against the gate at ±20 x and ±50 y.
		near := charX >= gate.x-20 && charX < gate.x+20 && charY >= gate.y-50 && charY < gate.y+50
		if int(x) == gate.x && int(y) == gate.y && gate.usableBy(id, party) && near {
			return other
		}
	}
	return nil
}

// useOpenGate moves a user standing at one gate of a pair to the other. The
// client teleports itself after sending 0xE8 and waits for its exclusive
// request flag to clear, so every outcome answers with enableActions.
func (s *Server) useOpenGate(live *liveCharacter, sink packetSink, channel int, u protocol.UseOpenGate) error {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	to := s.gameplay.openGateTarget(channel, u.OwnerID, u.X, u.Y, c.MapID, c.X, c.Y, c.ID, s.partyOf(c.ID), time.Now())
	if to == nil {
		slog.Warn("open_gate_use_rejected", "character", c.ID, "owner", u.OwnerID, "x", u.X, "y", u.Y, "map", c.MapID, "char_x", c.X, "char_y", c.Y)
		return sink.WritePacket(protocol.EnableActions())
	}
	live.mu.Lock()
	if live.session.Character.MapID == to.mapID {
		live.session.Character.X, live.session.Character.Y = to.x, to.y
	}
	live.mu.Unlock()
	slog.Info("open_gate_used", "character", c.ID, "owner", u.OwnerID, "x", to.x, "y", to.y)
	return sink.WritePacket(protocol.EnableActions())
}
