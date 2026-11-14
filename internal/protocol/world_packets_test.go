package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestJavaStarterStatsAndLook(t *testing.T) {
	// The golden bytes are the stock four-byte EXP layout.
	defer domain.SetLongExperienceForTest(false)()
	defer domain.SetLevel32ForTest(true)() // launcher level32 contract
	defer domain.SetLongStatsForTest(false)()
	data, err := os.ReadFile("testdata/starter_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		FixedMS int64  `json:"fixed_ms"`
		Stats   string `json:"stats"`
		Look    string `json:"look"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	stats := NewRawWriter()
	addCharStats(stats, starterCharacter(), time.UnixMilli(fixture.FixedMS))
	look := NewRawWriter()
	addCharLook(look, starterCharacter())
	for _, part := range []struct {
		name string
		got  []byte
		want string
	}{{"stats", stats.Packet(), fixture.Stats}, {"look", look.Packet(), fixture.Look}} {
		want, err := hex.DecodeString(part.want)
		if err != nil {
			t.Fatal(err)
		}
		if part.name == "stats" {
			want = v148StatsFromJava145(want)
		}
		if !bytes.Equal(part.got, want) {
			for i := 0; i < len(part.got) && i < len(want); i++ {
				if part.got[i] != want[i] {
					t.Fatalf("Java %s mismatch offset%d: got%02x want%02x", part.name, i, part.got[i], want[i])
				}
			}
			t.Fatalf("Java %s length got%d want%d", part.name, len(part.got), len(want))
		}
	}
}

// v148StatsFromJava145 widens the Legacy GW_CharacterStat golden to the v148
// layout: the 12-byte block at 119 becomes 21 bytes, and 68 zero bytes
// (u8, three i32, u8, six {i32,u8,i32}) precede the reversed time.
func v148StatsFromJava145(java []byte) []byte {
	const block, tail = 119 + 12, 119 + 12 + 4 + 1 + 4 + 1 + 4
	out := append([]byte(nil), java[:block]...)
	out = append(out, make([]byte, 9)...)
	out = append(out, java[block:tail]...)
	out = append(out, make([]byte, 68)...)
	return append(out, java[tail:]...)
}

func emptyCharacter() domain.Character { return domain.Character{} }
func starterCharacter() domain.Character {
	return domain.Character{ID: 1, AccountID: 2, Name: "Go145", WorldID: 0, Gender: 0, Skin: 0, Face: 20100, Hair: 30000, Level: 1, Job: 0, STR: 12, DEX: 5, INT: 4, LUK: 4, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000, Equipment: []domain.Equipment{{ID: 1, ItemID: 1040010, Slot: -5}, {ID: 2, ItemID: 1060002, Slot: -6}, {ID: 3, ItemID: 1072005, Slot: -7}, {ID: 4, ItemID: 1322005, Slot: -11, WATK: 18}}}
}
func TestStarterWorldPacketHasFullLayout(t *testing.T) {
	// The golden bytes are the stock four-byte balance layout.
	defer domain.SetLongMoneyForTest(false)()
	p := setFieldAt(starterCharacter(), 1, time.Unix(1793419200, 0), [12]byte{})
	if len(p) < 900 || len(p) > 2000 {
		t.Fatalf("unexpectedfullworldpayloadsize%d", len(p))
	}
	if binary.LittleEndian.Uint16(p) != SendSetField {
		t.Fatal("wrongopcode")
	}
	// SET_FIELD has its fixed header followed by random seeds and full mask.
	r := NewReader(p)
	r.Skip(2)
	if r.I32() != 0 {
		t.Fatal("wrongchannel")
	}
	r.Skip(1 + 4)
	if r.U8() != 1 {
		t.Fatal("notfullfield")
	}
	r.Skip(4)
	if r.U8() != 1 {
		t.Fatal("missingcharinfo")
	}
	if r.U16() != 0 {
		t.Fatal("unexpectedmessages")
	}
	r.Skip(12)
	if r.I64() != 0x1_ffffffff {
		t.Fatal("missingfullmask")
	}
	if SetField(starterCharacter(), 0) != nil {
		t.Fatal("acceptedchannel0")
	}
	invalid := starterCharacter()
	invalid.Mesos = 1 << 32
	if SetField(invalid, 1) != nil {
		t.Fatal("silentlytruncatedmesos")
	}
}
