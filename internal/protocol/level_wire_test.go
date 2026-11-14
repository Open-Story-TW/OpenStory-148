package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// The v148 client reads the stock level byte until the launcher's level32
// patch is ported; switching the contract must change only that field.
func TestCharacterStatsLevelWidthFollowsLevel32Contract(t *testing.T) {
	defer domain.SetIntLevelCapForTest(0)() // stock level cap
	c := starterCharacter()
	c.Level = 70
	now := time.Unix(1793419200, 0)
	restore := domain.SetLevel32ForTest(false)
	byteLevel := NewRawWriter()
	addCharStats(byteLevel, c, now)
	restore()
	defer domain.SetLevel32ForTest(true)()
	intLevel := NewRawWriter()
	addCharStats(intLevel, c, now)
	b, i := byteLevel.Packet(), intLevel.Packet()
	const at = 4 + 15 + 1 + 1 + 4 + 4
	if len(i) != len(b)+3 || b[at] != 70 || i[at] != 70 || i[at+1] != 0 || string(b[at+1:]) != string(i[at+4:]) {
		t.Fatalf("level width: byte=%x int=%x", b[:at+4], i[:at+4])
	}
}
