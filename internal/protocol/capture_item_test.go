package protocol

import (
	"encoding/hex"
	"testing"
)

func TestV148CaptureItemRequestAndEffect(t *testing.T) {
	w := NewWriter(RecvCaptureItem)
	w.U32(123)
	w.I16(5)
	w.I32(2270004)
	w.I32(42)
	packet := w.Packet()
	req, err := ReadCaptureItem(NewReader(packet[2:]))
	if err != nil || req != (CaptureItemRequest{Slot: 5, ItemID: 2270004, ObjectID: 42}) {
		t.Fatalf("capture request = %+v, err = %v", req, err)
	}
	if _, err := ReadCaptureItem(NewReader(packet[2 : len(packet)-1])); err == nil {
		t.Fatal("truncated capture request was accepted")
	}
	if got := hex.EncodeToString(CaptureMonsterResult(42, 2270004, true)); got != "bc012a00000034a3220001" {
		t.Fatalf("capture effect = %s", got)
	}
}
