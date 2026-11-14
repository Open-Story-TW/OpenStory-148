package protocol

import "testing"

func TestScriptPortalNativeFrameAndBoundaries(t *testing.T) {
	w := NewWriter(RecvScriptPortal)
	w.U8(34)
	w.String("in00")
	w.I16(70)
	w.I16(155)
	body := w.Packet()[2:]
	got, err := ReadScriptPortalRequest(NewReader(body))
	if err != nil || got.FieldKey != 34 || got.Name != "in00" || got.X != 70 || got.Y != 155 {
		t.Fatalf("native script portal frame: %+v %v", got, err)
	}
	for n := 0; n < len(body); n++ {
		if _, err = ReadScriptPortalRequest(NewReader(body[:n])); err == nil {
			t.Fatalf("accepted truncated frame at %d", n)
		}
	}
	if _, err = ReadScriptPortalRequest(NewReader(append(append([]byte(nil), body...), 0))); err == nil {
		t.Fatal("accepted unknown trailer")
	}
	for _, name := range []string{"", string(make([]byte, 65))} {
		w := NewWriter(RecvScriptPortal)
		w.U8(0)
		w.String(name)
		w.I16(0)
		w.I16(0)
		if _, err = ReadScriptPortalRequest(NewReader(w.Packet()[2:])); err == nil {
			t.Fatal("accepted invalid portal name length")
		}
	}
}
