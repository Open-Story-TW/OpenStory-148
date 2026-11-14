package world

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestQuestNotificationsUseActionTenNotStateValue(t *testing.T) {
	for _, tc := range []struct{ state, next, infoCount int }{{0, 0, 0}, {1, 0, 1}, {2, 0, 0}, {2, 1032, 1}} {
		value := domain.QuestResult{Quest: domain.QuestState{ID: 1031, State: tc.state}, NextQuest: tc.next}
		value.Character = domain.Character{Level: 1, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5}
		count := 0
		for _, packet := range questResultPackets(value, 2101) {
			if len(packet) < 2 || binary.LittleEndian.Uint16(packet) != protocol.SendQuestInfo {
				continue
			}
			count++
			if packet[2] != 10 {
				t.Fatal("quest state was encoded as a native quest action")
			}
		}
		if count != tc.infoCount {
			t.Fatalf("state%d next%d action notifications=%d", tc.state, tc.next, count)
		}
	}
}
