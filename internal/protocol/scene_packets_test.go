package protocol

import (
	"bytes"
	"testing"
)

func TestCutscenePacketShapes(t *testing.T) {
	for name, c := range map[string]struct{ got, want []byte }{
		"alone":  {SceneStandAlone(true), []byte{0x67, 0x01, 1}},
		"walk":   {SceneForcedInput(2), []byte{0x7C, 0x01, 3, 2, 0, 0, 0}},
		"delay":  {SceneDelay(1000), []byte{0x7C, 0x01, 1, 0xE8, 0x03, 0, 0}},
		"action": {SceneForcedAction(5, 256), []byte{0x7C, 0x01, 0, 5, 0, 0, 0, 0, 1, 0, 0}},
		"deck":   {PhantomDeck(7), []byte{0x82, 0x01, 7}},
	} {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got %x want %x", name, c.got, c.want)
		}
	}
	p := FieldScreenEffect("phantom/mapname1")
	r := NewReader(p[2:])
	if !bytes.Equal(p[:2], []byte{0xDF, 0x00}) || r.U8() != 3 || r.String() != "phantom/mapname1" || r.Remaining() != 0 {
		t.Fatalf("screen effect %x", p)
	}
	for _, bad := range []string{"", "a/../b", `a\b`, "a\x00b"} {
		if FieldScreenEffect(bad) != nil || SceneEffectAt(bad, 1, 0, 0) != nil || SceneImages(1402000, []string{bad}) != nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSceneSaySpeakers(t *testing.T) {
	// The player speaks: no close button (1) and the player's portrait (2).
	p := SceneSay(1402000, 0, "hi", false, true)
	r := NewReader(p[2:])
	if r.U8() != 4 || r.I32() != 1402000 || r.U8() != 0 || r.U8() != 3 || r.String() != "hi" || r.U8() != 0 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatalf("player line %x", p)
	}
	// Another NPC speaks: flag 4 carries its template id before the text.
	p = SceneSay(1402000, 1402400, "hi", true, true)
	r = NewReader(p[2:])
	if r.U8() != 4 || r.I32() != 1402000 || r.U8() != 0 || r.U8() != 5 || r.I32() != 1402400 || r.String() != "hi" || r.U8() != 1 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatalf("npc line %x", p)
	}
	p = SceneImages(1402000, []string{"UI/tutorial/phantom/0/0", "UI/tutorial/phantom/1/0"})
	r = NewReader(p[2:])
	if r.U8() != 4 || r.I32() != 1402000 || r.U8() != NPCTalkImages || r.U8() != 1 || r.U8() != 2 || r.String() != "UI/tutorial/phantom/0/0" || r.String() != "UI/tutorial/phantom/1/0" || r.Remaining() != 0 {
		t.Fatalf("images %x", p)
	}
}

func TestSceneAnswers(t *testing.T) {
	// The client reports a finished delay as type 0x11 with two more bytes.
	v, err := ReadNPCResponse(NewReader([]byte{NPCTalkSceneDelay, 1, 0}))
	if err != nil || v.Kind != "wait" || !v.Bool || v.Cancel {
		t.Fatalf("delay answer %+v %v", v, err)
	}
	v, err = ReadNPCResponse(NewReader([]byte{NPCTalkImages, 1}))
	if err != nil || v.Kind != "image" || !v.Bool || v.Cancel {
		t.Fatalf("image answer %+v %v", v, err)
	}
	v, err = ReadNPCResponse(NewReader([]byte{NPCTalkImages, 0xFF}))
	if err != nil || v.Kind != "image" || !v.Cancel {
		t.Fatalf("closed images %+v %v", v, err)
	}
}

func TestPhantomCardPacket(t *testing.T) {
	p := PhantomCard(12971, 1000005, 24100003, 9, 1)
	r := NewReader(p[2:])
	if !bytes.Equal(p[:2], []byte{0xF7, 0x00}) || r.U8() != 0 || r.I32() != 12971 || r.I32() != 1 || r.I32() != 1000005 || r.I32() != 24100003 ||
		r.U8() != 1 || r.I32() != 9 || r.I32() != 1 {
		t.Fatalf("card head %x", p)
	}
	r.Skip(12)
	if r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("card tail %x", p)
	}
}
