package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestGeneralChatOnlyReachesSelfAndVisibleSameChannelPlayers(t *testing.T) {
	maps := newMapSpace(800)
	self, peer, far, otherChannel := &memSink{}, &memSink{}, &memSink{}, &memSink{}
	a := domain.Character{ID: 1, Name: "A", MapID: 10000}
	maps.Enter(1, 10000, a, self)
	maps.Enter(1, 10000, domain.Character{ID: 2, Name: "B", X: 100}, peer)
	maps.Enter(1, 10000, domain.Character{ID: 3, Name: "Far", X: 5000}, far)
	maps.Enter(2, 10000, domain.Character{ID: 4, Name: "OtherChannel"}, otherChannel)
	self.pkts, peer.pkts, far.pkts, otherChannel.pkts = nil, nil, nil, nil
	s := &Server{maps: maps}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: a}}
	live.chat.staleUI() // the launcher already has the interface mask
	seedBoundScene(t, s, live, self)
	w := protocol.NewRawWriter()
	w.U32(100)
	w.String("兩人測試")
	w.U8(0)
	if err := s.handleGeneralChat(context.Background(), live, self, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
	if len(self.pkts) != 1 || len(peer.pkts) != 1 || len(far.pkts) != 0 || len(otherChannel.pkts) != 0 {
		t.Fatalf("chat visibility: self=%d peer=%d far=%d channel=%d", len(self.pkts), len(peer.pkts), len(far.pkts), len(otherChannel.pkts))
	}
	maps.Leave(1, 10000, 1)
	peer.pkts = nil
	if err := s.handleGeneralChat(context.Background(), live, self, protocol.NewReader(w.Packet())); err != nil || len(peer.pkts) != 0 {
		t.Fatal("departed sender could still broadcast")
	}
}

func TestChatBudgetRefillsWithoutAccumulatingBeyondBurst(t *testing.T) {
	var budget chatBudget
	now := time.Now()
	for range 4 {
		if !budget.take(now) {
			t.Fatal("initial burst rejected")
		}
	}
	if budget.take(now) || budget.take(now.Add(499*time.Millisecond)) {
		t.Fatal("unbounded chat burst")
	}
	if !budget.take(now.Add(500*time.Millisecond)) || budget.take(now.Add(500*time.Millisecond)) {
		t.Fatal("fractional refill changed")
	}
	for range 4 {
		if !budget.take(now.Add(time.Hour)) {
			t.Fatal("idle bucket did not refill")
		}
	}
	if budget.take(now.Add(time.Hour)) {
		t.Fatal("idle accumulated above capacity")
	}
}

func TestChatUnlimitedSkipsTheBudget(t *testing.T) {
	now := time.Now()
	defer domain.SetChatUnlimitedForTest(true)()
	var budget chatBudget
	for range 20 {
		if !budget.allow(now) {
			t.Fatal("unlimited chat was limited")
		}
	}
	domain.SetChatUnlimited(false)
	for range 4 {
		if !budget.allow(now) {
			t.Fatal("budget was spent while unlimited")
		}
	}
	if budget.allow(now) {
		t.Fatal("limit not restored")
	}
}

func TestChatCarriesAChangedInterfaceMask(t *testing.T) {
	previous := protocol.ClientUI()
	defer protocol.SetClientUI(previous)
	protocol.SetClientUI(protocol.ClientUIChatUnlimited)
	var budget chatBudget
	if !budget.staleUI() || budget.staleUI() {
		t.Fatal("first chat must carry the mask once")
	}
	protocol.SetClientUI(0)
	if !budget.staleUI() || budget.staleUI() {
		t.Fatal("a changed mask (even zero) must be carried once")
	}
}

func TestGeneralChatSendsTheMaskBeforeTheLine(t *testing.T) {
	previous := protocol.ClientUI()
	defer protocol.SetClientUI(previous)
	protocol.SetClientUI(protocol.ClientUIChatUnlimited)
	maps := newMapSpace(800)
	self := &memSink{}
	a := domain.Character{ID: 1, Name: "A", MapID: 10000}
	maps.Enter(1, 10000, a, self)
	self.pkts = nil
	s := &Server{maps: maps}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: a}}
	seedBoundScene(t, s, live, self)
	w := protocol.NewRawWriter()
	w.U32(100)
	w.String("hi")
	w.U8(0)
	for range 2 {
		if err := s.handleGeneralChat(context.Background(), live, self, protocol.NewReader(w.Packet())); err != nil {
			t.Fatal(err)
		}
	}
	if len(self.pkts) != 3 || string(self.pkts[0]) != string(protocol.ClientUIPacket()) {
		t.Fatalf("packets=%x", self.pkts)
	}
}
