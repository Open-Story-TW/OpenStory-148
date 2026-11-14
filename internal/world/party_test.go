package world

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type partyStub struct {
	domain.Repository
	states map[int64]domain.PartyState
	result domain.PartyMutation
	last   domain.PartyRequest
	calls  int
	mutate func(domain.PartyRequest) (domain.PartyMutation, error)
}

func (p *partyStub) ReadParty(_ context.Context, s domain.CharacterSession) (domain.PartyState, error) {
	return p.states[s.CharacterID], nil
}
func (p *partyStub) MutateParty(_ context.Context, q domain.PartyRequest) (domain.PartyMutation, error) {
	p.calls++
	p.last = q
	if p.mutate != nil {
		return p.mutate(q)
	}
	return p.result, nil
}

type partyChannelSink struct {
	writes  chan []byte
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func partySink() *partyChannelSink { return &partyChannelSink{writes: make(chan []byte, 100)} }
func (p *partyChannelSink) WritePacket(packet []byte) error {
	if p.started != nil {
		p.once.Do(func() { close(p.started); <-p.unblock })
	}
	p.writes <- append([]byte(nil), packet...)
	return nil
}
func (p *partyChannelSink) read(t *testing.T) []byte {
	t.Helper()
	select {
	case packet := <-p.writes:
		return packet
	case <-time.After(time.Second):
		t.Fatal("party delivery timeout")
		return nil
	}
}

func partyRuntime(id int64, name string, channel, field int) *liveCharacter {
	return &liveCharacter{session: domain.CharacterSession{CharacterID: id, AccountID: id, SessionID: "party-live", Generation: 1, ChannelID: channel, Lease: domain.WorldLease{WorldID: 1}, Character: domain.Character{ID: id, WorldID: 1, Name: name, Level: 10, Job: 100, MapID: field, HP: 25, MaxHP: 50}}}
}
func partyRuntimeSnapshot() domain.PartySnapshot {
	return domain.PartySnapshot{ID: 7, WorldID: 1, LeaderID: 11, Revision: 1, Members: []domain.PartyMember{{CharacterID: 11, Name: "Alice", Slot: 0, Level: 99, Job: 999, MapID: 999, HP: 999, MaxHP: 999}, {CharacterID: 22, Name: "Bob", Slot: 1, Level: 99, Job: 999, MapID: 999, HP: 999, MaxHP: 999}}}
}

func TestPartyLivePresenceOverridesSQLAndOldCleanupCannotEraseReplacement(t *testing.T) {
	p := partyRuntimeSnapshot()
	a, b := partyRuntime(11, "Alice", 1, 100), partyRuntime(22, "Bob", 2, 200)
	repo := &partyStub{states: map[int64]domain.PartyState{11: {Party: p}, 22: {Party: p}}}
	s := &Server{repo: repo, parties: newPartySpace(), lease: domain.WorldLease{WorldID: 1}}
	first, second := partySink(), partySink()
	if err := s.enterParty(context.Background(), a, first); err != nil {
		t.Fatal(err)
	}
	first.read(t)
	if err := s.enterParty(context.Background(), b, second); err != nil {
		t.Fatal(err)
	}
	first.read(t)
	second.read(t)
	s.parties.mu.Lock()
	visible := s.parties.visible(p)
	s.parties.mu.Unlock()
	if visible.Members[0].HP != 25 || visible.Members[0].MaxHP != 50 || visible.Members[1].Channel != 2 || visible.Members[1].MapID != 200 || visible.Members[1].Level != 10 || visible.Members[1].Job != 100 {
		t.Fatalf("stale SQL state %+v", visible)
	}
	s.leaveParty(b)
	offline := first.read(t)
	if got := int32(binary.LittleEndian.Uint32(offline[7+186+4:])); got != -1 {
		t.Fatalf("offline channel%d", got)
	}
	replacement := partyRuntime(22, "Bob", 3, 300)
	third := partySink()
	if err := s.enterParty(context.Background(), replacement, third); err != nil {
		t.Fatal(err)
	}
	first.read(t)
	third.read(t)
	s.leaveParty(b)
	s.parties.mu.Lock()
	visible = s.parties.visible(p)
	same := s.parties.peers[22].live == replacement
	s.parties.mu.Unlock()
	if !same || visible.Members[1].Channel != 3 {
		t.Fatal("late cleanup erased replacement")
	}
	s.leaveParty(a)
	s.leaveParty(replacement)
}

func TestPartyFIFOBlockedOldSnapshotThenNewMapAndDeparture(t *testing.T) {
	p := partyRuntimeSnapshot()
	a, b := partyRuntime(11, "Alice", 1, 100), partyRuntime(22, "Bob", 1, 200)
	blocked := partySink()
	blocked.started = make(chan struct{})
	blocked.unblock = make(chan struct{})
	other := partySink()
	space := newPartySpace()
	first := newPartyPeer(a, blocked, p)
	second := newPartyPeer(b, other, p)
	defer first.halt()
	defer second.halt()
	space.peers[11], space.peers[22] = first, second
	s := &Server{parties: space}
	space.mu.Lock()
	first.party = space.visible(p)
	second.party = first.party
	queueParty(space.snapshots(p))
	space.mu.Unlock()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not block")
	}
	if !space.commands.TryLock() {
		t.Fatal("socket write held command mutex")
	}
	space.commands.Unlock()
	a.mu.Lock()
	a.session.Character.MapID = 300
	a.mu.Unlock()
	finished := make(chan struct{})
	go func() { s.refreshParty(a); s.leaveParty(b); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("slow recipient blocked presence transitions")
	}
	close(blocked.unblock)
	old, newer, last := blocked.read(t), blocked.read(t), blocked.read(t)
	mapAt := func(raw []byte) uint32 { return binary.LittleEndian.Uint32(raw[7+238:]) }
	if mapAt(old) != 100 || mapAt(newer) != 300 || mapAt(last) != 300 || int32(binary.LittleEndian.Uint32(last[7+186+4:])) != -1 {
		t.Fatal("old delivery overwrote newer native party state")
	}
	// XY-only changes cause no party packets and never call a repository.
	a.mu.Lock()
	a.session.Character.X = 800
	a.session.Character.Y = 900
	a.mu.Unlock()
	s.refreshParty(a)
	select {
	case <-blocked.writes:
		t.Fatal("XY-only movement emitted snapshot")
	default:
	}
}

func TestPartyInviteUsesActualOnlineNameAndAckNeverPublishesJoin(t *testing.T) {
	a, b := partyRuntime(11, "Alice", 1, 100), partyRuntime(22, "Bob", 2, 200)
	first, second := partySink(), partySink()
	space := newPartySpace()
	pa, pb := newPartyPeer(a, first, domain.PartySnapshot{}), newPartyPeer(b, second, domain.PartySnapshot{})
	defer pa.halt()
	defer pb.halt()
	space.peers[11], space.peers[22] = pa, pb
	repo := &partyStub{result: domain.PartyMutation{Action: domain.PartyAcknowledge}}
	s := &Server{repo: repo, parties: space, lease: domain.WorldLease{WorldID: 1}}
	if err := s.handlePartyReply(context.Background(), b, second, protocol.NewReader([]byte{26, 7, 0, 0, 0})); err != nil {
		t.Fatal(err)
	}
	if repo.last.Action != domain.PartyAcknowledge || space.peers[22].party.ID != 0 {
		t.Fatal("26 became acceptance")
	}
	select {
	case <-second.writes:
		t.Fatal("ack emitted a membership packet")
	default:
	}
	w := protocol.NewRawWriter()
	w.U8(4)
	w.String("Offline")
	if err := s.handleParty(context.Background(), a, first, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
	first.read(t)
	if repo.calls != 1 {
		t.Fatal("offline invite reached persistence")
	}
	w = protocol.NewRawWriter()
	w.U8(4)
	w.String("bOb")
	if err := s.handleParty(context.Background(), a, first, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
	if repo.last.Name != "Bob" || repo.last.TargetID != 22 {
		t.Fatal("invite did not bind actual peer", repo.last)
	}
}

func TestPartyDatabaseCommandDoesNotBlockRuntimeRefresh(t *testing.T) {
	p := partyRuntimeSnapshot()
	a, b := partyRuntime(11, "Alice", 1, 100), partyRuntime(22, "Bob", 1, 200)
	first, second := partySink(), partySink()
	space := newPartySpace()
	pa, pb := newPartyPeer(a, first, p), newPartyPeer(b, second, p)
	defer pa.halt()
	defer pb.halt()
	space.peers[11], space.peers[22] = pa, pb
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	repo := &partyStub{mutate: func(q domain.PartyRequest) (domain.PartyMutation, error) {
		close(started)
		<-release
		return domain.PartyMutation{Action: q.Action, Before: p, After: p}, nil
	}}
	s := &Server{repo: repo, parties: space, lease: domain.WorldLease{WorldID: 1}}
	go func() {
		done <- s.applyParty(context.Background(), a, first, domain.PartyRequest{Action: domain.PartySetting})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("database call not reached")
	}
	b.mu.Lock()
	b.session.Character.MapID = 300
	b.mu.Unlock()
	refreshed := make(chan struct{})
	go func() { s.refreshParty(b); close(refreshed) }()
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("SQL held presence mutex")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
