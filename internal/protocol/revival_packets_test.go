package protocol

import "testing"

func nativeRevivalBody(wheel uint32, crc bool) []byte {
	w := NewWriter(RecvChangeMap)
	w.U8(1) // field key; this is not an authoritative death-state flag.
	w.I32(0)
	w.String("")
	w.U8(0)
	w.U32(wheel)
	if crc {
		w.U8(1)
		w.I32(123)
		w.I32(456)
	} else {
		w.U8(0)
	}
	return w.Packet()[2:]
}

func TestNativeRevivalRequestStrictShape(t *testing.T) {
	for _, crc := range []bool{false, true} {
		body := nativeRevivalBody(0, crc)
		q, err := ReadPortalRequest(NewReader(body))
		if err != nil || q.Revival == nil || q.Revival.WheelType != 0 || q.TargetMap != 0 || q.Name != "" {
			t.Fatalf("crc=%v request=%+v err=%v", crc, q, err)
		}
		for n := 0; n < len(body); n++ {
			if _, err = ReadPortalRequest(NewReader(body[:n])); err == nil {
				t.Fatalf("accepted truncated native revival length=%d/%d", n, len(body))
			}
		}
		if _, err = ReadPortalRequest(NewReader(append(body, 0))); err == nil {
			t.Fatal("accepted trailing revival data")
		}
	}
	// Paid choices remain represented so the host can reject them explicitly.
	q, err := ReadPortalRequest(NewReader(nativeRevivalBody(3, false)))
	if err != nil || q.Revival == nil || q.Revival.WheelType != 3 {
		t.Fatalf("wheel choice lost: %+v %v", q, err)
	}
	body := nativeRevivalBody(0, false)
	body[len(body)-1] = 2
	if _, err = ReadPortalRequest(NewReader(body)); err == nil {
		t.Fatal("accepted unknown CRC discriminator")
	}
}
