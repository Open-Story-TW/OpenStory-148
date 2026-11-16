package world

import (
	"testing"
	"time"
)

func TestHeliosElevatorStartsWithFirstPassenger(t *testing.T) {
	now := time.Unix(1000, 0)
	station := mapKey{channel: 1, mapID: heliosElevators[0].dock}
	elevators := elevatorSpace{starts: make(map[mapKey]time.Time)}
	elevators.start(station, now)
	elevators.start(station, now.Add(10*time.Second))
	for _, step := range []struct {
		delay time.Duration
		phase transitPhase
	}{
		{elevatorWait - time.Nanosecond, transitBoarding},
		{elevatorWait, transitAway},
		{elevatorWait + elevatorRide - time.Nanosecond, transitAway},
		{elevatorWait + elevatorRide, transitBoarding},
	} {
		if got := elevators.phase(station, now.Add(step.delay)); got != step.phase {
			t.Errorf("phase at %v = %q, want %q", step.delay, got, step.phase)
		}
		if got := elevators.phase(mapKey{channel: 2, mapID: station.mapID}, now.Add(step.delay)); got != transitBoarding {
			t.Errorf("phase in other channel = %q", got)
		}
	}
}
