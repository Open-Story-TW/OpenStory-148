package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func domainLook(face, hair int) domain.Character { return domain.Character{Face: face, Hair: hair} }

func TestNPCPromptWireContract(t *testing.T) {
	for _, tc := range []struct {
		kind, text, want string
		choices          []NPCMenuChoice
	}{
		{"say", "Hi", "410204d10700000000020048690001", nil},
		{"yes_no", "OK?", "410204d1070000020003004f4b3f", nil},
		{"menu", "Pick", "410204d107000005000f005069636b0d0a234c3723596573236c", []NPCMenuChoice{{7, "Yes"}}},
	} {
		want, e := hex.DecodeString(tc.want)
		if e != nil {
			t.Fatal(e)
		}
		got := NPCPrompt(2001, tc.kind, tc.text, tc.choices)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s got %x want %x", tc.kind, got, want)
		}
	}
}

func TestNPCMapSelectionWireContract(t *testing.T) {
	got := NPCPrompt(9010022, "map_selection", "", []NPCMenuChoice{{801, "A"}, {2, "B"}})
	// op, speaker type 4, npc, type 0x10, flag 0, Neo City 0, mirror 1, "#801#A#2#B"
	want, _ := hex.DecodeString("410204667b8900" + "1000" + "00000000" + "01000000" + "0a00" + hex.EncodeToString([]byte("#801#A#2#B")))
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
	for _, bad := range [][]NPCMenuChoice{nil, {{1, "a#b"}}, {{1, ""}}, {{1, "a"}, {1, "b"}}} {
		if NPCPrompt(9010022, "map_selection", "", bad) != nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	for _, tc := range []struct {
		raw    []byte
		cancel bool
		choice int64
	}{
		{[]byte{0x10, 1, 0x21, 3, 0, 0}, false, 801},
		{[]byte{0x10, 0}, true, 0},
	} {
		v, e := ReadNPCResponse(NewReader(tc.raw))
		if e != nil || v.Kind != "map_selection" || v.Cancel != tc.cancel || v.Choice != tc.choice {
			t.Fatalf("%x %+v %v", tc.raw, v, e)
		}
	}
	for _, raw := range [][]byte{{0x10, 1}, {0x10, 1, 1}, {0x10, 1, 255, 255, 255, 255}, {0x10, 2}} {
		if _, e := ReadNPCResponse(NewReader(raw)); e == nil {
			t.Fatalf("accepted %x", raw)
		}
	}
}

func TestNPCResponseRejectsWrongShape(t *testing.T) {
	for _, tc := range []struct {
		raw    []byte
		kind   string
		cancel bool
		choice int64
	}{
		{[]byte{0, 1}, "say", false, 0},
		{[]byte{2, 0}, "yes_no", false, 0},
		{[]byte{2, 1}, "yes_no", false, 0},
		{[]byte{5, 1, 7, 0, 0, 0}, "menu", false, 7},
		{[]byte{0, 255}, "say", true, 0},
	} {
		v, e := ReadNPCResponse(NewReader(tc.raw))
		if e != nil || v.Kind != tc.kind || v.Cancel != tc.cancel || v.Choice != tc.choice {
			t.Fatalf("%x %+v %v", tc.raw, v, e)
		}
	}
	for _, raw := range [][]byte{{}, {0}, {0, 2}, {0, 1, 0}, {3, 1}, {5, 1}, {5, 1, 255, 255, 255, 255}} {
		if _, e := ReadNPCResponse(NewReader(raw)); e == nil {
			t.Fatalf("accepted %x", raw)
		}
	}
}

func TestNPCScriptableUsesNativeCalendarDates(t *testing.T) {
	p := NPCScriptable([]NPCScript{{TemplateID: 2100, Name: "2100"}, {TemplateID: 2101, Name: "2101"}})
	r := NewReader(p)
	if r.U16() != 0x1db || r.U8() != 2 {
		t.Fatal("wrong scriptable header")
	}
	for _, id := range []int32{2100, 2101} {
		if r.I32() != id || len(r.String()) != 4 || r.I32() != 20000101 || r.I32() != 20991231 {
			t.Fatal("NPC registration must use inclusive YYYYMMDD dates")
		}
	}
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("wrong scriptable record length")
	}
}

func TestNPCPreviousIsDistinctFromCancel(t *testing.T) {
	for _, tc := range []struct {
		mode                  byte
		forward, back, cancel bool
	}{{1, true, false, false}, {0, false, true, false}, {255, false, false, true}} {
		value, err := ReadNPCResponse(NewReader([]byte{0, tc.mode}))
		if err != nil || value.Bool != tc.forward || value.Back != tc.back || value.Cancel != tc.cancel {
			t.Fatalf("native mode%d: %+v %v", tc.mode, value, err)
		}
	}
}

func TestNPCAvatarPromptAndAnswer(t *testing.T) {
	got := NPCPromptButtons(1012103, "avatar", "Hi", []NPCMenuChoice{{ID: 30030}, {ID: 30031}}, false, true)
	// header, speaker 4, NPC, ASK_AVATAR 9, param 0, text, U8 count, I32 styles.
	want, _ := hex.DecodeString("4102" + "04" + "87710f00" + "09" + "00" + "0200" + "4869" + "02" + "4e750000" + "4f750000")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
	if NPCPromptButtons(1012103, "avatar", "Hi", nil, false, true) != nil || NPCPromptButtons(1012103, "avatar", "Hi", []NPCMenuChoice{{ID: 1302000}}, false, true) != nil {
		t.Fatal("empty or non-style list accepted")
	}
	for _, tc := range []struct {
		raw    []byte
		cancel bool
		choice int64
		bad    bool
	}{
		{[]byte{9, 1, 3}, false, 3, false},
		{[]byte{9, 1, 3, 0, 0, 0}, false, 3, false},
		{[]byte{9, 0}, true, 0, false},
		{[]byte{9, 255}, true, 0, false},
		{[]byte{9, 1}, false, 0, true},
		{[]byte{9, 1, 0xff, 0xff, 0xff, 0xff}, false, 0, true},
	} {
		v, err := ReadNPCResponse(NewReader(tc.raw))
		if tc.bad {
			if err == nil {
				t.Fatalf("%x accepted", tc.raw)
			}
			continue
		}
		if err != nil || v.Kind != "avatar" || v.Cancel != tc.cancel || v.Choice != tc.choice {
			t.Fatalf("%x -> %+v %v", tc.raw, v, err)
		}
	}
}

func TestUpdateLookStatsMask(t *testing.T) {
	got := hex.EncodeToString(UpdateLookStats(domainLook(20001, 30030), true, true))
	if got != "2c0001"+"0600000000000000"+"214e0000"+"4e750000"+"0000" {
		t.Fatalf("got %s", got)
	}
}
