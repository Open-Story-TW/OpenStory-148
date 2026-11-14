package world

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type buddyTestRepository struct {
	domain.Repository
	lists  map[int64]domain.BuddyList
	result domain.BuddyMutation
	err    error
	calls  int
}

func (r *buddyTestRepository) ReadBuddies(_ context.Context, s domain.CharacterSession) (domain.BuddyList, error) {
	return r.lists[s.CharacterID], r.err
}
func (r *buddyTestRepository) MutateBuddy(_ context.Context, q domain.BuddyRequest) (domain.BuddyMutation, error) {
	r.calls++
	return r.result, r.err
}

func buddyLive(id int64, channel int) *liveCharacter {
	return &liveCharacter{session: domain.CharacterSession{CharacterID: id, AccountID: id, SessionID: "buddy-test", Generation: 1, ChannelID: channel, Character: domain.Character{ID: id, Name: "Hero"}}}
}

func TestBuddyPresenceRequiresMutualAcceptanceAndTracksChannels(t *testing.T) {
	b := newBuddySpace()
	a := buddyLive(1, 1)
	bLive := buddyLive(2, 2)
	aList := domain.BuddyList{Capacity: 232, Entries: []domain.BuddyEntry{{CharacterID: 2, Name: "Other", Group: "Friends"}}}
	bList := domain.BuddyList{Capacity: 232, Entries: []domain.BuddyEntry{{CharacterID: 1, Name: "Hero", Group: "Friends", Pending: true}}}
	repo := &buddyTestRepository{lists: map[int64]domain.BuddyList{1: aList, 2: bList}}
	s := &Server{repo: repo, buddies: b}
	first, second := &memSink{}, &memSink{}
	if err := s.enterBuddies(context.Background(), a, first); err != nil {
		t.Fatal(err)
	}
	if err := s.enterBuddies(context.Background(), bLive, second); err != nil {
		t.Fatal(err)
	}
	if got := b.visibleList(1, aList).Entries[0].Channel; got != 0 {
		t.Fatal("unaccepted invitation revealed presence")
	}
	if len(second.pkts) != 3 || second.pkts[2][2] != 9 {
		t.Fatal("offline invitation not restored")
	}
	if second.pkts[1][2] != 7 || second.pkts[1][3] != 0 {
		t.Fatal("native action9 appends pending entry; initial snapshot must exclude it")
	}
	bList.Entries[0].Pending = false
	b.peers[2].list = bList
	if got := b.visibleList(1, aList).Entries[0].Channel; got != 2 {
		t.Fatalf("cross-channel friend hidden: %d", got)
	}
	first.pkts = nil
	s.leaveBuddies(bLive)
	if len(first.pkts) != 1 || first.pkts[0][2] != 20 || int32(binary.LittleEndian.Uint32(first.pkts[0][8:])) != -1 {
		t.Fatal("disconnect did not announce offline")
	}
	// A source connection's late cleanup cannot remove the replacement.
	replacement := buddyLive(2, 3)
	b.peers[2] = &buddyPeer{live: replacement, channel: 3, list: bList, sink: second}
	s.leaveBuddies(bLive)
	if b.peers[2] == nil || b.visibleList(1, aList).Entries[0].Channel != 3 {
		t.Fatal("old connection erased new channel presence")
	}
}

func TestBuddyMutationPublishesOnlyAfterCommitAndDoesNotUseMapAOI(t *testing.T) {
	a, bLive := buddyLive(1, 1), buddyLive(2, 2)
	first, second := &memSink{}, &memSink{}
	aList := domain.BuddyList{Capacity: 232, Entries: []domain.BuddyEntry{{CharacterID: 2, Name: "Other", Group: "Friends"}}}
	bList := domain.BuddyList{Capacity: 232, Entries: []domain.BuddyEntry{{CharacterID: 1, Name: "Hero", Group: "Friends", Pending: true}}}
	repo := &buddyTestRepository{result: domain.BuddyMutation{List: aList, TargetID: 2, TargetList: bList, Invitation: true, SenderName: "Hero", SenderLevel: 12, SenderJob: 100}}
	s := &Server{repo: repo, buddies: newBuddySpace()}
	s.buddies.peers[1] = &buddyPeer{live: a, sink: first, channel: 1, list: domain.BuddyList{Capacity: 232}}
	s.buddies.peers[2] = &buddyPeer{live: bLive, sink: second, channel: 2, list: domain.BuddyList{Capacity: 232}}
	w := protocol.NewRawWriter()
	w.U8(1)
	w.String("Other")
	w.String("Friends")
	if err := s.handleBuddy(context.Background(), a, first, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
	if len(first.pkts) != 1 || len(second.pkts) != 1 || second.pkts[0][2] != 9 {
		t.Fatal("mutation not delivered to both channels")
	}
	first.pkts, second.pkts = nil, nil
	repo.err = domain.ErrBuddyTargetFull
	if err := s.handleBuddy(context.Background(), a, first, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
	if len(first.pkts) != 1 || first.pkts[0][2] != 12 || len(second.pkts) != 0 {
		t.Fatal("failed transaction published recipient state")
	}
	repo.err = errors.New("database failure")
	if err := s.handleBuddy(context.Background(), a, first, protocol.NewReader(w.Packet())); err == nil {
		t.Fatal("DB error was misreported as missing character")
	}
}
