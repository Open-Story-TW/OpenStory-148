package world

import (
	"bytes"
	"testing"

	"maple-go-server/internal/protocol"
)

func TestParsePreferenceChat(t *testing.T) {
	for _, tc := range []struct {
		text    string
		handled bool
		packet  []byte
	}{
		{"hello", false, nil},
		{"@unknown 1", false, nil},
		{"!level 10", false, nil},
		{"@設定", true, nil},
		{"@fps 60", true, protocol.ClientPreferencePacket(protocol.ClientPrefMaxFPS, 60)},
		{"@fps 241", true, nil},
		{"@fps", true, nil},
		{"@解析度 1280x720", true, protocol.ClientPreferencePacket(protocol.ClientPrefResolution, 2)},
		{"@res 1768*992", true, protocol.ClientPreferencePacket(protocol.ClientPrefResolution, 5)},
		{"@解析度 640x480", true, nil},
		{"@視窗 關", true, protocol.ClientPreferencePacket(protocol.ClientPrefWindowed, 0)},
		{"@顯示 怪物 關", true, protocol.ClientPreferencePacket(protocol.ClientPrefRenderLayer, 5)},
		{"@顯示 NPC 開", true, protocol.ClientPreferencePacket(protocol.ClientPrefRenderLayer, 6|1<<8)},
		{"@顯示 月亮 開", true, nil},
		{"@背景輸入 開", true, protocol.ClientPreferencePacket(protocol.ClientPrefBackgroundInput, 1)},
		{"@重設", true, protocol.ClientPreferencePacket(protocol.ClientPrefReset, 0)},
	} {
		handled, packet, reply := parsePreferenceChat(tc.text)
		if handled != tc.handled || !bytes.Equal(packet, tc.packet) {
			t.Errorf("%q: handled=%v packet=%x, want handled=%v packet=%x", tc.text, handled, packet, tc.handled, tc.packet)
		}
		if handled && reply == "" {
			t.Errorf("%q: a handled command needs a reply", tc.text)
		}
	}
}
