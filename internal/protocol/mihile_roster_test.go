package protocol

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

// One character outside ValidJob makes CharacterList return nil, which hides
// the account's whole roster. Every Mihile job must render beside the others.
func TestCharacterListKeepsRosterWithMihileJobs(t *testing.T) {
	defer domain.SetLevel32ForTest(true)()
	defer domain.SetLongStatsForTest(false)()
	other := domain.CharacterSummary{ID: 3, Name: "RosterHero", Job: 112, Level: 120, STR: 9, DEX: 4, INT: 4, LUK: 4, HP: 500, MaxHP: 500, BaseMaxHP: 500, MP: 100, MaxMP: 100, BaseMaxMP: 100, VitalsModel: 1, SP: 1, MapID: 100000000}
	for _, job := range []int{5000, 5100, 5110, 5111, 5112} {
		mihile := other
		mihile.ID, mihile.Name, mihile.Job = 4, "RosterMihile", job
		packet := CharacterList([]domain.CharacterSummary{other, mihile}, 3)
		if len(packet) < 4 || binary.LittleEndian.Uint16(packet[:2]) != SendCharacterList || packet[3] != 2 {
			t.Fatalf("Mihile job %d hid the character list", job)
		}
		if len(SetField(mihile, 1)) == 0 {
			t.Fatalf("Mihile job %d cannot enter the world", job)
		}
	}
	for _, job := range []int{5001, 5101, 5113, 5200} {
		bad := other
		bad.Job = job
		if CharacterList([]domain.CharacterSummary{bad}, 3) != nil {
			t.Fatalf("roster allowed nonexistent job %d", job)
		}
	}
}
