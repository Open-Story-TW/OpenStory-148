package world

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The two switches follow TWMS Reactor.wz/2309000.img. Hitting either
// powers the spring as in the original 2309000 reactor script.
const ghostSwitchTime = 3 * time.Second

type ghostDeck struct {
	switches [2]*roomReactor
	resetAt  [2]time.Time
	spring   *roomReactor
}

func (g *gameplaySpace) prepareGhostDecks(channel int, course content.GhostRoom) error {
	defs, err := content.GhostDeckReactors(wzRoot())
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for team := range content.GhostTeams {
		room := g.rooms[mapKey{channel, course.Course(team, content.GhostStages-1)}]
		if room == nil {
			return domain.ErrNotFound
		}
		if room.reactors == nil {
			room.reactors = make(map[int32]*roomReactor)
		}
		var actors [3]*roomReactor
		for i, def := range defs {
			oid := g.alloc()
			if oid == 0 {
				return domain.ErrInvalidInput
			}
			reactor := &roomReactor{objectID: oid, def: def}
			actors[i], room.reactors[oid] = reactor, reactor
		}
		room.ghostDeck = &ghostDeck{switches: [2]*roomReactor{actors[0], actors[1]}, spring: actors[2]}
	}
	return nil
}

func ghostReactorState(x *roomReactor) []byte {
	return protocol.ReactorState(x.objectID, x.state, x.def.X, x.def.Y)
}

// resetPackets runs while gameplaySpace.mu is held. Once launched, the
// spring stays available even after the timer returns a switch to rest.
func (d *ghostDeck) resetPackets(now time.Time) [][]byte {
	if d == nil {
		return nil
	}
	var packets [][]byte
	for i, x := range d.switches {
		if x.state == 1 && !now.Before(d.resetAt[i]) {
			x.state = 0
			packets = append(packets, ghostReactorState(x))
		}
	}
	return packets
}

func (g *gameplaySpace) ghostDeckReady(channel, mapID int, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	r := g.rooms[mapKey{channel, mapID}]
	if r == nil || r.ghostDeck == nil {
		return false
	}
	d := r.ghostDeck
	return now.Before(d.resetAt[0]) && now.Before(d.resetAt[1])
}

func (s *Server) hitGhostDeck(channel int, actor domain.Character, oid int32) bool {
	course, ok := content.GhostRoomOf(actor.MapID)
	if !ok || (actor.MapID != course.Course(0, content.GhostStages-1) && actor.MapID != course.Course(1, content.GhostStages-1)) {
		return false
	}
	inst := s.eventOf(actor.ID)
	if inst == nil || inst.ghost == nil || inst.channel != channel || actor.HP <= 0 || !time.Now().Before(inst.ghostEndTime()) {
		return false
	}
	g := s.gameplay
	g.mu.Lock()
	r := g.rooms[mapKey{channel, actor.MapID}]
	if r == nil || r.ghostDeck == nil {
		g.mu.Unlock()
		return false
	}
	d := r.ghostDeck
	var delivery []roomDelivery
	for i, x := range d.switches {
		if x.objectID != oid {
			continue
		}
		now := time.Now()
		if within(actor.X, actor.Y, x.def.X, x.def.Y, 125) && !now.Before(d.resetAt[i]) {
			x.state = 1
			d.resetAt[i] = now.Add(ghostSwitchTime)
			delivery = roomBroadcast(r, ghostReactorState(x), -1)
			if d.spring.state != 1 {
				d.spring.state = 1
				delivery = append(delivery, roomBroadcast(r, ghostReactorState(d.spring), -1)...)
			}
		}
		g.mu.Unlock()
		deliver(delivery)
		return true
	}
	g.mu.Unlock()
	return false
}
