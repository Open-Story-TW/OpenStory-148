package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/skills"
)

func TestSkillBuffRowsAllowsInt32TwoStateHomingBeacon(t *testing.T) {
	now := time.Unix(1700000000, 0)
	// Monster object IDs start at 1,000,000 (> 32,767), and HOMING_BEACON (bit 253)
	// is a TwoStateGuided stat encoded as I32 on the wire (0x6F4235).
	rows, ok := skillBuffRows(22151002, 10, &skills.Buff{
		Duration: skills.Permanent,
		Stats:    []skills.StatValue{{Stat: "HOMING_BEACON", Value: 1001630}},
	}, now)
	if !ok || len(rows) != 1 || rows[0].Stat != "HOMING_BEACON" || rows[0].Value != 1001630 {
		t.Fatalf("skillBuffRows HOMING_BEACON mob object id: ok=%v rows=%+v", ok, rows)
	}
	// Ordinary I16 local triplet stats without ForcesInt must still reject values > 32767.
	if _, ok = skillBuffRows(1001003, 1, &skills.Buff{
		Duration: time.Minute,
		Stats:    []skills.StatValue{{Stat: "WDEF", Value: 40000}},
	}, now); ok {
		t.Fatal("skillBuffRows must still reject WDEF > 32767")
	}
}
