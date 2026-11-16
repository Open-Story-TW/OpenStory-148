package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

type transitLeg struct {
	dock, boarding, ride, arrival int
	usher, attendant              int
}

type transitLine struct {
	departure time.Duration
	duration  time.Duration
	legs      [2]transitLeg
}

var transitLines = []transitLine{
	// The train and Leafre service use a three-minute turnaround. The ushers
	// close the waiting room one minute before departure.
	{3 * time.Minute, 5 * time.Minute, [2]transitLeg{
		{dock: 200000121, boarding: 200000122, ride: 200090100, arrival: 220000110, usher: 2012013, attendant: 2041001},
		{dock: 220000110, boarding: 220000111, ride: 200090110, arrival: 200000121, usher: 2041000, attendant: 2041001},
	}},
	{3 * time.Minute, 5 * time.Minute, [2]transitLeg{
		{dock: 200000131, boarding: 200000132, ride: 200090200, arrival: 240000100, usher: 2012021, attendant: 2012022},
		{dock: 240000110, boarding: 240000111, ride: 200090210, arrival: 200000100, usher: 2082001, attendant: 2082002},
	}},
}

type transitPhase string

const (
	transitBoarding  transitPhase = "boarding"
	transitDeparting transitPhase = "departing"
	transitAway      transitPhase = "away"
)

func (line transitLine) phase(start, now time.Time) transitPhase {
	elapsed := max(now.Sub(start), 0) % (line.departure + line.duration)
	switch {
	case elapsed < line.departure-time.Minute:
		return transitBoarding
	case elapsed < line.departure:
		return transitDeparting
	default:
		return transitAway
	}
}

func (s *Server) runTransit(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.tickTransit(ctx, now)
			s.tickElevators(ctx, now)
		}
	}
}

func (s *Server) tickTransit(ctx context.Context, now time.Time) {
	for _, line := range transitLines {
		phase := line.phase(s.transitStart, now)
		for _, leg := range line.legs {
			from, to := leg.ride, leg.arrival
			if phase == transitAway {
				from, to = leg.boarding, leg.ride
			}
			for channel := 1; channel <= s.wcfg.ChannelCount; channel++ {
				s.moveTransitPassengers(ctx, channel, from, to)
			}
		}
	}
}

func (s *Server) moveTransitPassengers(ctx context.Context, channel, from, to int) {
	for _, passenger := range s.eventOccupants(channel, []int{from}) {
		guard := func(session domain.CharacterSession) bool {
			return session.ChannelID == channel && session.Character.MapID == from
		}
		// A failed checkpoint is retried while the passenger remains in
		// the original waiting room or vehicle.
		_, _ = s.moveTransit(ctx, channel, passenger, to, guard)
	}
}

func (s *Server) moveTransit(ctx context.Context, channel int, passenger eventOccupant, to int, guard func(domain.CharacterSession) bool) (bool, error) {
	destination, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return false, err
	}
	entry, ok := destination.Portal("sp")
	if !ok {
		return false, domain.ErrNotFound
	}
	if err := s.warpGameplayConditional(ctx, channel, passenger.live, passenger.sink, to, entry, nil, guard, nil); err != nil {
		return false, err
	}
	passenger.live.mu.Lock()
	arrived := passenger.live.session.Character.MapID == to
	passenger.live.mu.Unlock()
	return arrived, nil
}

func (a *worldScript) transitCommand(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	if a.kind != "npc" || a.server.transitStart.IsZero() || len(command.Ints) != 0 || command.Text != "" {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	result := func(value any) (scripting.CommandResult, error) { return scripting.CommandResult{Value: value}, nil }
	from := snapshot.Character.MapID
	for _, line := range transitLines {
		for _, leg := range line.legs {
			dock := a.npcID == leg.usher && from == leg.dock
			waitingRoom := a.npcID == leg.attendant && from == leg.boarding
			switch command.Kind {
			case "transit_status":
				if dock {
					return result(string(line.phase(a.server.transitStart, time.Now())))
				}
				continue
			case "transit_board":
				if !dock {
					continue
				}
			case "transit_leave":
				if !waitingRoom {
					continue
				}
			}
			to := leg.boarding
			if waitingRoom {
				to = leg.dock
			}
			guard := func(session domain.CharacterSession) bool {
				if session.ChannelID != snapshot.ChannelID || session.Character.MapID != from || session.Character.HP <= 0 {
					return false
				}
				phase := line.phase(a.server.transitStart, time.Now())
				return (dock && phase == transitBoarding) || (waitingRoom && phase != transitAway)
			}
			moved, err := a.server.moveTransit(ctx, snapshot.ChannelID, eventOccupant{a.live, a.sink}, to, guard)
			return scripting.CommandResult{Value: moved}, err
		}
	}
	if command.Kind == "transit_status" {
		return result("unavailable")
	}
	return result(false)
}
