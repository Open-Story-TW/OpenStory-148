package protocol

import (
	"bytes"
	"maple-go-server/internal/domain"
	"testing"
)

func TestRewardNotificationsMatchLegacyStatusInfo(t *testing.T) {
	// The golden bytes are the stock four-byte EXP layout.
	defer domain.SetLongExperienceForTest(false)()
	// Java CWvsContext.InfoPacket, non-chat item/meso and white EXP paths.
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"item", ShowItemGain(2000000, 7), []byte{0x36, 0x00, 0, 0, 0x80, 0x84, 0x1e, 0, 7, 0, 0, 0}},
		{"mesos", ShowMesoGain(1234), []byte{0x36, 0x00, 0, 1, 0, 0xd2, 4, 0, 0, 0, 0}},
		{"experience", ShowExperienceGain(12345, true), append([]byte{0x36, 0x00, 3, 1, 0x39, 0x30, 0, 0, 0}, make([]byte, 68)...)},
		{"party experience", ShowExperienceGain(12345, false), append([]byte{0x36, 0x00, 3, 0, 0x39, 0x30, 0, 0, 0}, make([]byte, 68)...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !bytes.Equal(c.got, c.want) {
				t.Fatalf("status-info wire: %x, want %x", c.got, c.want)
			}
		})
	}
}

func TestRewardNotificationsRejectNonPositiveOrUnrepresentableAmounts(t *testing.T) {
	// The golden bytes are the stock four-byte EXP layout.
	defer domain.SetLongExperienceForTest(false)()
	for _, n := range []int64{-1, 0, 2147483648} {
		if ShowMesoGain(n) != nil || ShowExperienceGain(n, true) != nil || ShowItemGain(2000000, int(n)) != nil {
			t.Fatalf("invalid gain %d emitted a notification", n)
		}
	}
	if ShowItemGain(0, 1) != nil || ShowItemGain(-1, 1) != nil {
		t.Fatal("invalid item emitted notification")
	}
}

func TestExperienceNotificationMatchesNativeNonChatDecoder(t *testing.T) {
	// The golden bytes are the stock four-byte EXP layout.
	defer domain.SetLongExperienceForTest(false)()
	// CWvsContext::OnMessage 0x9481DD reads kind3, then 0x94758F:
	// white, gain, chat, event, rate, reserved, wedding, party, optional bytes,
	// one unconditional byte and 12 bonus I32s (13 in v148 0x9E22E0). No optional
	// bytes on this path.
	r := NewReader(ShowExperienceGain(77, true)[2:])
	if r.U8() != 3 || r.U8() != 1 || r.I32() != 77 || r.U8() != 0 || r.I32() != 0 || r.U8() != 0 || r.U8() != 0 || r.I32() != 0 || r.I32() != 0 || r.U8() != 0 {
		t.Fatal("native EXP base/branch fields differ")
	}
	for i := 0; i < 13; i++ {
		if r.I32() != 0 {
			t.Fatalf("unexpected separate bonus %d", i)
		}
	}
	if r.Err() != nil || r.Remaining() != 1 || r.U8() != 0 {
		t.Fatal("truncated native tail or wrong Java trailing byte")
	}
}
