package protocol

import (
	"bytes"
	"encoding/hex"
	"maple-go-server/internal/domain"
	"testing"
)

func TestScrollRequestAndEquipmentUpgradeMetadata(t *testing.T) {
	data, _ := hex.DecodeString("010000000200f5ff000000")
	r, err := ReadScrollRequest(NewReader(data))
	if err != nil || r.ClientTick != 1 || r.SourceSlot != 2 || r.TargetSlot != -11 || r.WhiteFlags != 0 || r.LegendarySpirit {
		t.Fatalf("scroll=%+v %v", r, err)
	}
	for n := 0; n < len(data); n++ {
		if _, err = ReadScrollRequest(NewReader(data[:n])); err == nil {
			t.Fatalf("truncated scroll accepted%d", n)
		}
	}
	w := NewRawWriter()
	addEquipment(w, domain.Equipment{ItemID: 1322005, UpgradeSlots: 5, ScrollLevel: 2, Flags: 6})
	p := NewReader(w.Packet())
	p.Skip(18)
	if p.U8() != 5 || p.U8() != 2 {
		t.Fatal("equipment slots/scroll level")
	}
	p.Skip(30)
	if p.String() != "" || p.I32() != 6 {
		t.Fatal("equipment flag offset")
	}
}

func TestScrollEffectNativeDecoderFixture(t *testing.T) {
	// CUserPool0x8AE194 consumes characterID then delegates to0x83DFA3:
	// status8, legendary8, scroll32, equipment32, flag8, flag8. No extraI32.
	want, _ := hex.DecodeString("0601010000000100402d1f00152c14000000")
	got := ScrollEffect(1, domain.ScrollResult{Status: 1, ScrollID: 2043200, EquipmentTemplateID: 1322005})
	if !bytes.Equal(got, want) {
		t.Fatalf("native scroll effect got%x want%x", got, want)
	}
}
