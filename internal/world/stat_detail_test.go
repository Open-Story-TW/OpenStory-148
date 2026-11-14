package world

import (
	"encoding/binary"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestPublishStatDetailResendsOnlyOnChange(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	t.Cleanup(func() { domain.SetRates(1, 1, 1) })
	domain.SetRates(1, 1, 1)
	now := time.Unix(1700000000, 0)
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, Level: 10, HP: 50, MaxHP: 50}}}
	sink, s := &memSink{}, &Server{skills: newSkillSpace()}
	sent := func() int { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.pkts) }

	s.publishStatDetail(live, sink, now, false)
	if sent() != 1 || len(sink.pkts[0]) != 83 || binary.LittleEndian.Uint16(sink.pkts[0]) != protocol.SendStatDetail {
		t.Fatalf("first publication: %d packets %x", sent(), sink.pkts)
	}
	s.publishStatDetail(live, sink, now, false)
	if sent() != 1 {
		t.Fatalf("an unchanged detail was sent again")
	}
	s.publishStatDetail(live, sink, now, true) // field entry
	if sent() != 2 {
		t.Fatalf("field entry did not send the detail")
	}
	// A buff arrives, then runs out.
	live.mu.Lock()
	live.session.Character.SkillBuffs = []domain.SkillBuff{{ID: 3121002, Stat: "SHARP_EYES", Value: 20, ExpiresAt: now.Add(time.Second)}}
	live.mu.Unlock()
	s.publishStatDetail(live, sink, now, false)
	s.publishStatDetail(live, sink, now, false)
	if sent() != 3 {
		t.Fatalf("buff gain: %d packets", sent())
	}
	s.publishStatDetail(live, sink, now.Add(2*time.Second), false)
	if sent() != 4 {
		t.Fatalf("buff expiry: %d packets", sent())
	}
	// A runtime rate change.
	domain.SetRates(2, 1, 1)
	s.publishStatDetail(live, sink, now.Add(2*time.Second), false)
	if sent() != 5 || binary.LittleEndian.Uint32(sink.pkts[4][3:]) != 200 {
		t.Fatalf("rate change: %d packets", sent())
	}
}
