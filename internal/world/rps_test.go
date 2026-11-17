package world

import "testing"

func TestRPSHandResult(t *testing.T) {
	game := &rpsGame{ready: true, paid: true}
	if packet := game.choose(0, 0); packet[4] != 0 || !game.ready || game.won {
		t.Fatalf("draw consumed the round: %x %+v", packet, game)
	}
	if packet := game.choose(0, 2); packet[4] != 1 || game.ready || !game.won || game.wins != 1 {
		t.Fatalf("win did not set first prize: %x %+v", packet, game)
	}
	game.ready, game.won = true, false
	if packet := game.choose(2, 0); packet[4] != 255 || game.ready || game.won {
		t.Fatalf("loss should close the round: %x %+v", packet, game)
	}
}
