package world

import (
	"testing"

	"maple-go-server/internal/content"
)

// A node stands a few pixels above its platform; its yield has to land on the
// platform line itself or the client draws it behind the tiles.
func TestGroundBelowFindsThePlatformUnderANode(t *testing.T) {
	m := content.Map{Footholds: []content.Foothold{
		{ID: 1, X1: 0, Y1: 510, X2: 400, Y2: 510},
		{ID: 2, X1: 400, Y1: 510, X2: 500, Y2: 460}, // slope
		{ID: 3, X1: 100, Y1: 210, X2: 300, Y2: 210}, // platform above
		{ID: 4, X1: 600, Y1: 300, X2: 600, Y2: 500}, // wall
	}}
	for _, c := range []struct {
		x, y, want int
		ok         bool
	}{
		{200, 505, 510, true}, // node on the ground, under the upper platform
		{200, 207, 210, true}, // node on the upper platform
		{200, 515, 510, true}, // a node sunk a few pixels still finds its ground
		{450, 480, 485, true}, // on the slope
		{550, 400, 0, false},  // off every platform
		{600, 400, 0, false},  // a wall is not ground
	} {
		got, ok := groundBelow(m, c.x, c.y)
		if ok != c.ok || got != c.want {
			t.Errorf("groundBelow(%d,%d) = %d %v, want %d %v", c.x, c.y, got, ok, c.want, c.ok)
		}
	}
}
