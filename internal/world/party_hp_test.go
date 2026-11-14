package world

import (
	"maple-go-server/internal/domain"
	"testing"
)

func TestPartyHPDeltasUseActualPartyMapChannelAndSpawnVisibility(t *testing.T) {
	p := partyRuntimeSnapshot()
	a, b := partyRuntime(11, "Alice", 1, 100), partyRuntime(22, "Bob", 1, 100)
	space := newPartySpace()
	pa, pb := newPartyPeer(a, partySink(), p), newPartyPeer(b, partySink(), p)
	defer pa.halt()
	defer pb.halt()
	space.peers[11], space.peers[22] = pa, pb
	maps := newMapSpace(800)
	maps.Enter(1, 100, a.session.Character, partySink())
	maps.Enter(1, 100, b.session.Character, partySink())
	s := &Server{parties: space, maps: maps}
	space.mu.Lock()
	visible := space.visible(p)
	out := s.partyHPDeltas(visible)
	if len(out) != 2 || out[0].packets[0][0] != 0x47 {
		t.Fatal("initial pair HP missing")
	}
	if len(s.partyHPDeltas(visible)) != 0 {
		t.Fatal("unchanged HP spam")
	}
	visible.Members[0].HP = 0
	if len(s.partyHPDeltas(visible)) != 1 {
		t.Fatal("death HP must publish")
	}
	visible.Members[0].MaxHP = 0
	if len(s.partyHPDeltas(visible)) != 0 {
		t.Fatal("zero divisor published")
	}
	visible = space.visible(p)
	s.partyHPDeltas(visible)
	// A stranger with the same map/channel never gets this party's vitals.
	stranger := partyRuntime(33, "Stranger", 1, 100)
	pc := newPartyPeer(stranger, partySink(), domain.PartySnapshot{})
	defer pc.halt()
	space.peers[33] = pc
	if len(s.partyHPDeltas(visible)) != 0 || len(pc.hpSeen) != 0 {
		t.Fatal("stranger received HP")
	}
	visible.Members[1].Channel = 2
	if len(s.partyHPDeltas(visible)) != 0 || len(pa.hpSeen) != 0 || len(pb.hpSeen) != 0 {
		t.Fatal("crosschannel HP leaked")
	}
	visible = space.visible(p)
	visible.Members[1].MapID = 200
	if len(s.partyHPDeltas(visible)) != 0 {
		t.Fatal("crossmap HP leaked")
	}
	space.mu.Unlock()
	// Leaving AOI forgets the remote object; reentry resends even unchanged HP.
	look := b.session.Character
	look.X = 2000
	maps.Move(1, 100, 22, look, 0, 0, nil)
	space.mu.Lock()
	if len(s.partyHPDeltas(space.visible(p))) != 0 || len(pa.hpSeen) != 0 {
		t.Fatal("unspawned remote got HP")
	}
	space.mu.Unlock()
	look.X = 0
	maps.Move(1, 100, 22, look, 0, 0, nil)
	space.mu.Lock()
	if len(s.partyHPDeltas(space.visible(p))) != 2 {
		t.Fatal("AOI reentry omitted unchanged HP")
	}
	space.mu.Unlock()
}
