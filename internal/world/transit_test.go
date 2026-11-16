package world

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/scripting"
)

func TestTransitTrainSchedule(t *testing.T) {
	line := transitLines[0]
	start := time.Unix(1000, 0)
	for _, test := range []struct {
		elapsed time.Duration
		want    transitPhase
	}{
		{-time.Second, transitBoarding},
		{0, transitBoarding},
		{2*time.Minute - time.Nanosecond, transitBoarding},
		{2 * time.Minute, transitDeparting},
		{3 * time.Minute, transitAway},
		{8*time.Minute - time.Nanosecond, transitAway},
		{8 * time.Minute, transitBoarding},
		{10 * time.Minute, transitDeparting},
	} {
		if got := line.phase(start, start.Add(test.elapsed)); got != test.want {
			t.Errorf("at %v got %q, want %q", test.elapsed, got, test.want)
		}
	}
}

func TestTransitMovesBothDirectionsAndRetriesCheckpoint(t *testing.T) {
	line := transitLines[0]
	start := time.Now()
	f := eventTestFixture(t)
	s := f.server
	s.wcfg = platform.WorldConfig{ChannelCount: 1}
	s.transitStart = start
	root := t.TempDir()
	mapDir := filepath.Join(root, "Map.wz", "Map", "Map2")
	if err := os.MkdirAll(mapDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, leg := range line.legs {
		for _, id := range []int{leg.dock, leg.boarding, leg.ride, leg.arrival} {
			name := strconv.Itoa(id) + ".img"
			data := `<imgdir name="` + name + `"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="0"/><int name="y" value="0"/></imgdir></imgdir></imgdir>`
			if err := os.WriteFile(filepath.Join(mapDir, name+".xml"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var err error
	s.gameplay.catalog, err = content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, leg := range line.legs {
		f.join(t, int64(i+1), 35, leg.dock, domain.PartySnapshot{})
	}
	f.join(t, 3, 35, line.legs[0].dock, domain.PartySnapshot{})
	f.join(t, 4, 35, line.legs[0].dock, domain.PartySnapshot{})
	ctx := context.Background()
	board := func(id int64, npc int) bool {
		a := &worldScript{server: s, live: f.lives[id], sink: f.sinks[id], kind: "npc", npcID: npc}
		result, err := a.transitCommand(ctx, partySession(a.live), scripting.Command{Kind: "transit_board"})
		if err != nil {
			t.Fatal(err)
		}
		return result.Value == true
	}
	if !board(1, line.legs[0].usher) || !board(2, line.legs[1].usher) {
		t.Fatal("both scheduled legs should admit passengers")
	}
	s.transitStart = start.Add(-line.departure + time.Minute - time.Second)
	if board(3, line.legs[0].usher) {
		t.Fatal("a closed station should reject admission")
	}
	s.transitStart = start
	if board(4, line.legs[1].usher) {
		t.Fatal("a mismatched usher should reject admission")
	}
	depart := start.Add(line.departure)
	delayed := true
	s.repo = gameplayTestRepo{save: func(session domain.CharacterSession, value domain.Character) error {
		if delayed && session.CharacterID == 1 && value.MapID == line.legs[0].ride {
			delayed = false
			return errors.New("temporary save failure")
		}
		return nil
	}}
	s.tickTransit(ctx, depart.Add(-time.Nanosecond))
	if got := f.mapOf(1); got != line.legs[0].boarding {
		t.Fatalf("map before departure = %d", got)
	}
	s.tickTransit(ctx, depart)
	if f.mapOf(1) != line.legs[0].boarding || f.mapOf(2) != line.legs[1].ride {
		t.Fatalf("checkpoint failure affected both legs: %d, %d", f.mapOf(1), f.mapOf(2))
	}
	s.tickTransit(ctx, depart.Add(time.Second))
	if got := f.mapOf(1); got != line.legs[0].ride {
		t.Fatalf("failed ride was not retried: map %d", got)
	}
	s.tickTransit(ctx, depart.Add(line.duration))
	if f.mapOf(1) != line.legs[0].arrival || f.mapOf(2) != line.legs[1].arrival || f.mapOf(3) != line.legs[0].dock || f.mapOf(4) != line.legs[0].dock {
		t.Fatalf("arrival should only move riders: %d, %d, %d, %d", f.mapOf(1), f.mapOf(2), f.mapOf(3), f.mapOf(4))
	}
}
