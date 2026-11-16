package world

import (
	"context"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type elevatorSpace struct {
	mu     sync.Mutex
	starts map[mapKey]time.Time
}

const (
	elevatorWait = 50 * time.Second
	elevatorRide = 55 * time.Second
)

var heliosElevators = [...]transitLeg{
	{dock: 222020100, boarding: 222020110, ride: 222020111, arrival: 222020200},
	{dock: 222020200, boarding: 222020210, ride: 222020211, arrival: 222020100},
}

func (e *elevatorSpace) phase(station mapKey, now time.Time) transitPhase {
	e.mu.Lock()
	defer e.mu.Unlock()
	start := e.starts[station]
	if start.IsZero() || !now.Before(start.Add(elevatorWait+elevatorRide)) {
		delete(e.starts, station)
		return transitBoarding
	}
	if now.Before(start.Add(elevatorWait)) {
		return transitBoarding
	}
	return transitAway
}

func (e *elevatorSpace) start(station mapKey, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	start := e.starts[station]
	if start.IsZero() || !now.Before(start.Add(elevatorWait+elevatorRide)) {
		e.starts[station] = now
	}
}

func (s *Server) tickElevators(ctx context.Context, now time.Time) {
	if s.elevators == nil {
		return
	}
	for _, leg := range heliosElevators {
		for channel := 1; channel <= s.wcfg.ChannelCount; channel++ {
			station := mapKey{channel: channel, mapID: leg.dock}
			moving := s.elevators.phase(station, now) == transitAway
			s.updateElevatorDoor(station, moving)
			if moving {
				s.moveTransitPassengers(ctx, channel, leg.boarding, leg.ride)
				continue
			}
			s.moveTransitPassengers(ctx, channel, leg.ride, leg.arrival)
			if len(s.eventOccupants(channel, []int{leg.boarding})) > 0 {
				// Passengers who log back in to the waiting room can still ride
				// after a world restart.
				s.elevators.start(station, now)
			}
		}
	}
}

func (s *Server) updateElevatorDoor(station mapKey, closed bool) {
	state := 0
	if closed {
		state = 1
	}
	var packets [][]byte
	var visits []*sceneVisit
	s.gameplay.mu.Lock()
	if room := s.gameplay.rooms[station]; room != nil {
		for _, reactor := range room.reactors {
			if reactor.def.TemplateID == content.HeliosElevatorDoor && reactor.state != state {
				reactor.state = state
				packets = append(packets, protocol.ReactorState(reactor.objectID, state, reactor.def.X, reactor.def.Y))
			}
		}
		if len(packets) > 0 {
			for _, member := range room.members {
				visits = append(visits, member.visit)
			}
		}
	}
	s.gameplay.mu.Unlock()
	for _, visit := range visits {
		_ = s.writeScenePackets(visit, packets...)
	}
}

func (h *portalScriptHost) boardElevator(ctx context.Context, command scripting.Command) error {
	if command.Kind != "elevator_board" || len(command.Ints) != 0 || command.Text != "" || h.portal.Name != "in00" {
		return domain.ErrForbidden
	}
	s := h.server
	if s.elevators == nil {
		return domain.ErrNotFound
	}
	station := mapKey{channel: h.channel, mapID: h.mapID}
	for _, leg := range heliosElevators {
		if leg.dock != h.mapID {
			continue
		}
		guard := func(session domain.CharacterSession) bool {
			c := session.Character
			return session.ChannelID == h.channel && c.MapID == h.mapID && c.HP > 0 &&
				within(c.X, c.Y, h.portal.X, h.portal.Y, 150) && s.elevators.phase(station, time.Now()) != transitAway
		}
		moved, err := s.moveTransit(ctx, h.channel, eventOccupant{h.live, h.sink}, leg.boarding, guard)
		if err != nil {
			return err
		}
		if moved {
			s.elevators.start(station, time.Now())
			return nil
		}
		return h.sink.WritePacket(protocol.PortalTrainingMessage("電梯正在行駛，請稍後再試。"))
	}
	return domain.ErrForbidden
}
