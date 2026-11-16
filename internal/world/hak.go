package world

import (
	"context"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const hakDuration = time.Minute

type hakSeat struct {
	ride  int
	until time.Time
}

type hakSpace struct {
	mu    sync.Mutex
	seats map[int]*hakSeat
}

func (h *hakSpace) riding(channel, ride int, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	seat := h.seats[channel]
	return seat != nil && seat.ride == ride && (seat.until.IsZero() || now.Before(seat.until))
}

var hakRoutes = [...]struct {
	from, to, ride int
	fare           int64
}{
	{200000141, 250000100, 200090300, 6000},
	{250000100, 200000141, 200090310, 6000},
	{250000100, 251000000, 0, 1500},
	{251000000, 250000100, 0, 1500},
}

func (a *worldScript) boardHak(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	if a.npcID != 2090005 || a.kind != "npc" || command.Text != "" ||
		len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] >= int64(len(hakRoutes)) {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	route := hakRoutes[command.Ints[0]]
	if snapshot.Character.MapID != route.from || a.server.hak == nil {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	channel := snapshot.ChannelID
	guard := func(session domain.CharacterSession) bool {
		return session.ChannelID == channel && session.Character.MapID == route.from && session.Character.HP > 0
	}
	if route.ride == 0 {
		return a.takeTransitFare(ctx, command.ID, route.to, 0, route.fare, guard)
	}
	hak := a.server.hak
	hak.mu.Lock()
	if hak.seats[channel] != nil {
		hak.mu.Unlock()
		return scripting.CommandResult{Value: false}, nil
	}
	seat := &hakSeat{ride: route.ride}
	hak.seats[channel] = seat
	hak.mu.Unlock()

	result, err := a.takeTransitFare(ctx, command.ID, route.ride, 0, route.fare, guard)
	hak.mu.Lock()
	if result.Value == true {
		seat.until = time.Now().Add(hakDuration)
	} else {
		delete(hak.seats, channel)
	}
	hak.mu.Unlock()
	if err == nil && result.Value == true {
		err = a.server.writeScenePackets(a.server.sceneFor(a.live, a.sink), protocol.Clock(int(hakDuration/time.Second)))
	}
	return result, err
}

func (s *Server) tickHak(ctx context.Context, now time.Time) {
	hak := s.hak
	if hak == nil {
		return
	}
	for channel := 1; channel <= s.wcfg.ChannelCount; channel++ {
		for _, route := range hakRoutes {
			if route.ride == 0 || hak.riding(channel, route.ride, now) {
				continue
			}
			for _, passenger := range s.eventOccupants(channel, []int{route.ride}) {
				guard := func(session domain.CharacterSession) bool {
					return session.ChannelID == channel && session.Character.MapID == route.ride && !hak.riding(channel, route.ride, now)
				}
				_, _ = s.moveTransit(ctx, channel, passenger, route.to, guard)
			}
		}
		hak.mu.Lock()
		seat := hak.seats[channel]
		expired := seat != nil && !seat.until.IsZero() && !now.Before(seat.until)
		hak.mu.Unlock()
		if expired && len(s.eventOccupants(channel, []int{seat.ride})) == 0 {
			hak.mu.Lock()
			if hak.seats[channel] == seat {
				delete(hak.seats, channel)
			}
			hak.mu.Unlock()
		}
	}
}
