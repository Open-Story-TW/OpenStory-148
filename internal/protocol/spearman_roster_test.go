package protocol

import (
	"encoding/binary"
	"maple-go-server/internal/domain"
	"testing"
)

func TestCharacterListSpearmanRosterIsACompleteNativeFrame(t *testing.T) {
	defer domain.SetLevel32ForTest(true)() // launcher level32 contract
	defer domain.SetLongStatsForTest(false)()
	// A lean roster projection has worn/scalar data, no learned-skill/bag preload.
	// v148 dropped the i32 before the count, so offsets are four lower than v145.
	c := domain.CharacterSummary{ID: 3, Name: "QASpearman", Job: 130, Level: 30, STR: 9, DEX: 4, INT: 4, LUK: 4, HP: 2055, MaxHP: 2055, BaseMaxHP: 1713, MP: 100, MaxMP: 100, BaseMaxMP: 100, VitalsModel: 1, SP: 1, MapID: 103000003, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11}}}
	packet := CharacterList([]domain.CharacterSummary{c}, 3)
	if len(packet) < 43 || len(packet) > 65535 || binary.LittleEndian.Uint16(packet[:2]) != SendCharacterList || packet[2] != 0 || packet[3] != 1 {
		t.Fatal("reviewed130 produced nil/truncated character list")
	}
	if binary.LittleEndian.Uint32(packet[4:8]) != 3 || binary.LittleEndian.Uint32(packet[33:37]) != 30 || binary.LittleEndian.Uint16(packet[37:39]) != 130 {
		t.Fatal("native roster CID/level32/job16 fields differ")
	}
	// Stats block maxima are intrinsic on wire; native adds worn/passive effects.
	if binary.LittleEndian.Uint32(packet[51:55]) != 1713 {
		t.Fatal("roster effective maximum leaked into base-max wire")
	}
	// Every v145 job now renders (QA may set any job); only the reviewed 130
	// level floor and nonexistent jobs stay out of the roster.
	for _, invalid := range []struct{ job, level int }{{130, 29}, {133, 30}, {600, 30}, {3400, 30}} {
		bad := c
		bad.Job, bad.Level = invalid.job, invalid.level
		if CharacterList([]domain.CharacterSummary{bad}, 3) != nil {
			t.Fatalf("roster allowed job%d level%d", bad.Job, bad.Level)
		}
	}
	if c.Skills != nil || c.Items != nil || c.Quests != nil {
		t.Fatal("test must remain a lean SQL/Redis roster projection")
	}
}
