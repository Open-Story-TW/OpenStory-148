package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

type transitLeg struct {
	dock, boarding, ride, arrival int
	usher, attendant              int
	alternateUsher, ticket        int
	cabin                         int
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
	{5 * time.Minute, 7 * time.Minute, [2]transitLeg{
		{dock: 200000151, boarding: 200000152, ride: 200090400, arrival: 260000100, usher: 2012025, attendant: 2012024},
		{dock: 260000100, boarding: 260000110, ride: 200090410, arrival: 200000100, usher: 2102000, attendant: 2102001},
	}},
	{5 * time.Minute, time.Minute, [2]transitLeg{
		{dock: 103020000, boarding: 600010004, ride: 600010005, arrival: 600010001, usher: 9201068, attendant: 9201057, alternateUsher: 1052007, ticket: 4031711},
		{dock: 600010001, boarding: 600010002, ride: 600010003, arrival: 103020000, usher: 9201068, attendant: 9201057, ticket: 4031713},
	}},
	{3 * time.Minute, 5 * time.Minute, [2]transitLeg{
		{dock: 104020110, boarding: 104020111, ride: 200090010, arrival: 200000100, usher: 1032008, attendant: 1032009, cabin: 200090011},
		{dock: 200000111, boarding: 200000112, ride: 200090000, arrival: 104020110, usher: 2012001, attendant: 2012002, cabin: 200090001},
	}},
	{5 * time.Minute, time.Minute, [2]transitLeg{
		{dock: 103000000, boarding: 540010100, ride: 540010101, arrival: 540010000, usher: 9270041, attendant: 9270017, ticket: 4031731},
		{dock: 540010000, boarding: 540010001, ride: 540010002, arrival: 103000000, usher: 9270038, attendant: 9270018, ticket: 4031732},
	}},
}

type transitPhase string

const (
	transitBoarding  transitPhase = "boarding"
	transitDeparting transitPhase = "departing"
	transitAway      transitPhase = "away"
)

func (line transitLine) phase(start, now time.Time) transitPhase {
	elapsed := line.elapsed(start, now)
	switch {
	case elapsed < line.departure-time.Minute:
		return transitBoarding
	case elapsed < line.departure:
		return transitDeparting
	default:
		return transitAway
	}
}

func (line transitLine) elapsed(start, now time.Time) time.Duration {
	return max(now.Sub(start), 0) % (line.departure + line.duration)
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
			s.tickHak(ctx, now)
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
				if leg.cabin != 0 {
					s.updateBoatDock(channel, leg.dock, phase)
					switch {
					case phase != transitAway:
						s.clearBoatInvasion(channel, leg.ride)
						s.moveTransitPassengers(ctx, channel, leg.cabin, leg.arrival)
					case line.elapsed(s.transitStart, now) >= line.departure+time.Minute:
						s.spawnBoatInvasion(channel, leg.ride)
					}
				}
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
			dock := from == leg.dock && (a.npcID == leg.usher || a.npcID == leg.alternateUsher)
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
			if dock && leg.ticket != 0 {
				return a.takeTransitFare(ctx, command.ID, to, leg.ticket, 0, guard)
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

func (a *worldScript) takeTransitFare(ctx context.Context, op string, to, ticket int, mesos int64, guard func(domain.CharacterSession) bool) (scripting.CommandResult, error) {
	refused := scripting.CommandResult{Value: false}
	s := a.server
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return refused, domain.ErrForbidden
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	session := s.skillSession(a.live)
	if !guard(session) || !a.hasNPCContext(session) || s.tradeActive(a.live) {
		return refused, nil
	}
	visit := s.sceneFor(a.live, a.sink)
	if visit == nil {
		return refused, domain.ErrStaleOwner
	}
	field, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return refused, err
	}
	entry, ok := field.Portal("sp")
	if !ok {
		return refused, domain.ErrNotFound
	}
	request := domain.ScriptExchange{
		Session: session, OperationID: op, MesosDelta: -mesos,
		Travel: &domain.ScriptTravel{FromMap: session.Character.MapID, MapID: to, SpawnPoint: entry.ID, X: entry.X, Y: entry.Y},
	}
	if ticket != 0 {
		request.Items = []domain.ScriptItemDelta{{TemplateID: ticket, Quantity: -1}}
	}
	commitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	value, err := repo.ExecuteScriptExchange(commitCtx, request)
	if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrForbidden) {
		return refused, nil
	}
	if err != nil {
		return refused, err
	}
	if value.Character.MapID != to {
		return refused, domain.ErrStaleOwner
	}
	_, err = a.publishScriptTravel(ctx, session, value, visit)
	return scripting.CommandResult{Value: err == nil}, err
}
