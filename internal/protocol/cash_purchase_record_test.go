package protocol

import "testing"

func TestNativePurchaseRecordQueryIsNotAPurchase(t *testing.T) {
	for _, sn := range []int32{0, 10000049} {
		w := NewRawWriter()
		w.I32(sn)
		got, err := ReadNativeCashPurchaseRecord(NewReader(w.Packet()))
		if err != nil || got != int(sn) {
			t.Fatalf("query %d: %d %v", sn, got, err)
		}
	}
	for _, body := range [][]byte{{}, {1}, {1, 0, 0}, {1, 0, 0, 0, 0}, {255, 255, 255, 255}} {
		if _, err := ReadNativeCashPurchaseRecord(NewReader(body)); err == nil {
			t.Fatalf("invalid query accepted %x", body)
		}
	}
}

func TestNativeGiftAcknowledgementCarriesOnlySerial(t *testing.T) {
	w := NewRawWriter()
	w.I64(7000000123)
	if got, err := ReadNativeCashGiftAck(NewReader(w.Packet())); err != nil || got != 7000000123 {
		t.Fatalf("ack: %d %v", got, err)
	}
	note := NewRawWriter()
	note.U8(0)
	note.String("goqa01")
	note.String("ty")
	note.U8(1)
	note.I32(0)
	note.I64(7000000123)
	body := note.Packet()
	if len(body) != 26 {
		t.Fatalf("native thank-you note is 26 bytes for a 6+2 byte name/memo, got %d", len(body))
	}
	if got, err := ReadNativeGiftNote(NewReader(body)); err != nil || got.To != "goqa01" || got.Message != "ty" || !got.Fame || got.Serial != 7000000123 {
		t.Fatalf("note: %+v %v", got, err)
	}
	if _, err := ReadNativeGiftNote(NewReader(append([]byte{1}, body[1:]...))); err == nil {
		t.Fatal("note-delete action decoded as thank-you note")
	}
	for _, body := range [][]byte{{}, {1, 2, 3, 4}, make([]byte, 9)} {
		if _, err := ReadNativeCashGiftAck(NewReader(body)); err == nil {
			t.Fatalf("invalid ack accepted %x", body)
		}
	}
}
