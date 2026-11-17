package world

import (
	"bytes"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

func TestIceEscortResumesAtWZDeadline(t *testing.T) {
	sink := &gameplayTestSink{}
	deadline := time.Now()
	stage := &iceEscortRoom{
		mob:       &gameplayMob{objectID: 1, hp: 100},
		stopUntil: deadline,
	}
	server := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
		{channel: 1, mapID: content.IceKnightFirst}: {
			ice:     stage,
			members: map[int64]*gameplayMember{1: {sink: sink}},
		},
	}}}
	server.resumeIceEscorts(deadline.Add(-time.Millisecond))
	if len(sink.packets) != 0 || stage.stopUntil.IsZero() {
		t.Fatal("an escort resumed before its WZ stop elapsed")
	}
	server.resumeIceEscorts(deadline)
	server.resumeIceEscorts(deadline.Add(time.Millisecond))
	if len(sink.packets) != 1 || !bytes.Equal(sink.packets[0], protocol.ResumeEscort(1)) || !stage.stopUntil.IsZero() {
		t.Fatalf("escort resume packets = %x, remaining stop = %v", sink.packets, stage.stopUntil)
	}
}
