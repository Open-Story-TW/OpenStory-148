package protocol

import (
	"encoding/binary"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestLongStatsWidenBaseStatsAndAPOnly(t *testing.T) {
	c := domain.Character{STR: 50000, DEX: 40000, INT: 9999999, LUK: 32768, AP: 70000}

	restore := domain.SetLongStatsForTest(true)
	defer restore()
	packet := AttributeStats(c)
	// opcode 2, flag 1, mask 8, then five int32 values and two trailing bytes.
	if len(packet) != 2+1+8+5*4+2 {
		t.Fatalf("long AttributeStats length %d", len(packet))
	}
	for i, want := range []int{c.STR, c.DEX, c.INT, c.LUK, c.AP} {
		got := binary.LittleEndian.Uint32(packet[11+4*i:])
		if int(got) != want {
			t.Fatalf("field %d: got %d want %d", i, got, want)
		}
	}
	if got := len(JobAdvancementStats(c)); got != 43+5*2 {
		t.Fatalf("long JobAdvancementStats length %d", got)
	}

	restoreOff := domain.SetLongStatsForTest(false)
	defer restoreOff()
	short := domain.Character{STR: 300, DEX: 25, INT: 4, LUK: 4, AP: 7}
	packet = AttributeStats(short)
	if len(packet) != 2+1+8+5*2+2 {
		t.Fatalf("native AttributeStats length %d", len(packet))
	}
	if got := binary.LittleEndian.Uint16(packet[11:]); got != 300 {
		t.Fatalf("native STR %d", got)
	}
}

func TestLongStatsCharacterStatsAddFourBytesPerField(t *testing.T) {
	defer domain.SetLevel32ForTest(true)() // launcher level32 contract
	defer domain.SetLongStatsForTest(true)()
	c := domain.Character{ID: 1, Name: "Long", Level: 10, STR: 50000, DEX: 4, INT: 4, LUK: 4, AP: 40000}
	long := NewRawWriter()
	addCharStats(long, c, time.UnixMilli(0))
	longBytes := long.Packet()
	// ID 4, name 15, gender 1, skin 1, face 4, hair 4, level 4, job 2.
	const strAt = 35
	if got := binary.LittleEndian.Uint32(longBytes[strAt:]); got != 50000 {
		t.Fatalf("STR %d", got)
	}
	defer domain.SetLongStatsForTest(false)()
	native := NewRawWriter()
	addCharStats(native, domain.Character{ID: 1, Name: "Long", Level: 10, STR: 300, DEX: 4, INT: 4, LUK: 4, AP: 4}, time.UnixMilli(0))
	if len(longBytes)-len(native.Packet()) != 5*2 {
		t.Fatalf("width difference %d", len(longBytes)-len(native.Packet()))
	}
}

func TestMaxStatFollowsTheContract(t *testing.T) {
	defer domain.SetLongStatsForTest(true)()
	if domain.MaxStat() != 9_999_999 {
		t.Fatal(domain.MaxStat())
	}
	defer domain.SetLongStatsForTest(false)()
	if domain.MaxStat() != domain.NativeStatLimit {
		t.Fatal(domain.MaxStat())
	}
}
