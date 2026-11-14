package world

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type tradeTestRepository struct {
	domain.Repository
	mu      sync.Mutex
	calls   int
	request domain.TradeSettlement
}

func (r *tradeTestRepository) SettleTrade(_ context.Context, request domain.TradeSettlement) (domain.TradeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.request = request
	a, b := request.First.Character, request.Second.Character
	a.Revision++
	b.Revision++
	a.Mesos += request.SecondOffer.Mesos - request.FirstOffer.Mesos
	b.Mesos += request.FirstOffer.Mesos - request.SecondOffer.Mesos
	return domain.TradeResult{First: domain.InventoryResult{Character: a}, Second: domain.InventoryResult{Character: b}}, nil
}

func tradeFixture(t *testing.T) (server *Server, a, b *liveCharacter, as, bs *memSink, repo *tradeTestRepository) {
	repo = &tradeTestRepository{}
	server = &Server{repo: repo, trades: newTradeSpace(), online: make(map[int64]domain.CharacterSession)}
	a = &liveCharacter{session: domain.CharacterSession{AccountID: 1, CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, AccountID: 1, Name: "A", Level: 1, MapID: 10000, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, Mesos: 1000, Revision: 1, Items: []domain.InventoryItem{{ID: 1, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 5}}}}}
	b = &liveCharacter{session: domain.CharacterSession{AccountID: 2, CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, AccountID: 2, Name: "B", Level: 1, MapID: 10000, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, Mesos: 1000, Revision: 1, Items: []domain.InventoryItem{{ID: 2, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 5}}}}}
	as, bs = &memSink{}, &memSink{}
	server.tradeEnter(a, as)
	server.tradeEnter(b, bs)
	t.Cleanup(func() {
		server.trades.mu.Lock()
		var peers []*tradeParticipant
		for _, p := range server.trades.clients {
			p.halt()
			peers = append(peers, p)
		}
		server.trades.mu.Unlock()
		for _, p := range peers {
			p.closeSink()
		}
	})
	return
}

func tradeAction(t *testing.T, s *Server, live *liveCharacter, sink packetSink, action byte, body func(*protocol.Writer)) {
	t.Helper()
	w := protocol.NewRawWriter()
	w.U8(action)
	if body != nil {
		body(w)
	}
	if err := s.handleTrade(context.Background(), live, sink, protocol.NewReader(w.Packet())); err != nil {
		t.Fatal(err)
	}
}

func openTestTrade(t *testing.T, s *Server, a, b *liveCharacter, as, bs packetSink) {
	t.Helper()
	tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
	ref := s.trades.rooms[1].reference
	tradeAction(t, s, b, bs, 9, func(w *protocol.Writer) { w.U32(ref); w.Zero(2) })
}

func TestTradeRequiresBothConfirmationsAndCancelsCleanly(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	openTestTrade(t, s, a, b, as, bs)
	if !s.tradeActive(a) || !s.tradeActive(b) {
		t.Fatal("participants not reserved")
	}
	tradeAction(t, s, a, as, 0, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(2); w.U8(1) })
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(100) })
	if a.session.Character.Mesos != 1000 || a.session.Character.Items[0].Quantity != 5 {
		t.Fatal("unconfirmed offer debited authoritative assets")
	}
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(1); w.I32(2000000); w.U32(0) })
	if repo.calls != 0 {
		t.Fatal("single confirmation committed trade")
	}
	tradeAction(t, s, b, bs, 2, func(w *protocol.Writer) { w.U8(0) })
	if repo.calls != 1 {
		t.Fatalf("settlement calls=%d", repo.calls)
	}
	if a.session.Character.Mesos != 900 || b.session.Character.Mesos != 1100 || s.tradeActive(a) || s.tradeActive(b) {
		t.Fatal("completed trade left wrong live balances/state")
	}
	tradeAction(t, s, b, bs, 2, func(w *protocol.Writer) { w.U8(0) })
	if repo.calls != 1 {
		t.Fatal("duplicate confirmation settled again")
	}
	openTestTrade(t, s, a, b, as, bs)
	s.tradeLeave(a)
	if s.tradeActive(a) || s.tradeActive(b) || repo.calls != 1 {
		t.Fatal("disconnect did not cancel uncommitted trade")
	}
	if b.session.Character.Mesos != 1100 {
		t.Fatal("cancel changed assets")
	}
}

func TestTradeRejectsCrossChannelAndOverOffer(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	b.mu.Lock()
	b.session.ChannelID = 2
	b.mu.Unlock()
	tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
	if s.tradeActive(b) {
		t.Fatal("cross-channel invitation accepted")
	}
	tradeAction(t, s, a, as, 18, nil)
	b.mu.Lock()
	b.session.ChannelID = 1
	b.mu.Unlock()
	openTestTrade(t, s, a, b, as, bs)
	tradeAction(t, s, a, as, 0, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(6); w.U8(1) })
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(1001) })
	room := s.trades.rooms[1]
	if len(room.offers[0].Items) != 0 || room.offers[0].Mesos != 0 {
		t.Fatal("oversized offer reserved")
	}
	tradeAction(t, s, a, as, 18, nil)
	tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
	ref := s.trades.rooms[1].reference
	tradeAction(t, s, b, bs, 12, func(w *protocol.Writer) { w.U32(ref); w.U8(3) })
	if s.tradeActive(a) || s.tradeActive(b) || repo.calls != 0 {
		t.Fatal("decline did not clear both parties")
	}
}

func TestNativeOldTradeInvitationCannotAcceptOrDeclineReplacementRoom(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	invite := func() uint32 {
		tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
		tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
		return s.trades.rooms[1].reference
	}
	old := invite()
	tradeAction(t, s, a, as, 18, nil)
	current := invite()
	if current <= old {
		t.Fatal("room reference reused")
	}
	tradeAction(t, s, b, bs, 9, func(w *protocol.Writer) { w.U32(old); w.Zero(2) })
	if s.trades.rooms[1].accepted {
		t.Fatal("old invitation accepted new room")
	}
	tradeAction(t, s, b, bs, 12, func(w *protocol.Writer) { w.U32(old); w.U8(3) })
	if !s.tradeActive(a) || !s.tradeActive(b) {
		t.Fatal("old decline cancelled new invitation")
	}
	tradeAction(t, s, b, bs, 9, func(w *protocol.Writer) { w.U32(current); w.Zero(2) })
	if !s.trades.rooms[1].accepted || repo.calls != 0 {
		t.Fatal("correct native invitation did not open provisionally")
	}
}

func TestNativeTradeConfirmChecksCannotAuthorizeDifferentItemsAndAckIsNotConsent(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	openTestTrade(t, s, a, b, as, bs)
	tradeAction(t, s, a, as, 0, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(2); w.U8(1) })
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(1); w.I32(2000001); w.U32(1) })
	if s.trades.rooms[1].confirmed[0] || repo.calls != 0 {
		t.Fatal("mismatched native item list confirmed trade")
	}
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(1); w.I32(2000000); w.U32(0xdeadbeef) })
	tradeAction(t, s, b, bs, 5, func(w *protocol.Writer) { w.U8(1); w.I32(2000000); w.U32(0x12345678) })
	if repo.calls != 0 || s.trades.rooms[1].confirmed[1] {
		t.Fatal("automatic native peer-info ack was treated as player confirmation")
	}
	tradeAction(t, s, b, bs, 2, func(w *protocol.Writer) { w.U8(0) })
	if repo.calls != 1 {
		t.Fatal("two original confirmations did not settle exactly once")
	}
}

func TestTradeReplacementSessionCancelsOldRoomAndCannotBeMutatedByOldSocket(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	openTestTrade(t, s, a, b, as, bs)
	newSink := &memSink{}
	replacement := &liveCharacter{session: a.session}
	replacement.session.Generation++
	s.tradeEnter(replacement, newSink)
	if s.tradeActive(replacement) || s.tradeActive(b) {
		t.Fatal("replacement kept old session's provisional trade")
	}
	tradeAction(t, s, replacement, newSink, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	current := s.trades.rooms[1]
	tradeAction(t, s, a, as, 18, nil)
	s.tradeLeave(a)
	if s.trades.rooms[1] != current || current.closed || s.trades.clients[1].live != replacement || repo.calls != 0 {
		t.Fatal("late old socket removed replacement trade/presence")
	}
	if replacement.session.Character.Mesos != 1000 || b.session.Character.Mesos != 1000 {
		t.Fatal("provisional cleanup changed durable assets")
	}
}

func TestDeadTradeActorCannotOpenOrOfferAssets(t *testing.T) {
	s, a, b, as, bs, repo := tradeFixture(t)
	a.mu.Lock()
	a.session.Character.HP = 0
	a.mu.Unlock()
	tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	if s.tradeActive(a) {
		t.Fatal("dead actor opened trade")
	}
	a.mu.Lock()
	a.session.Character.HP = 50
	a.mu.Unlock()
	openTestTrade(t, s, a, b, as, bs)
	a.mu.Lock()
	a.session.Character.HP = 0
	a.mu.Unlock()
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(1) })
	if s.trades.rooms[1].offers[0].Mesos != 0 || repo.calls != 0 {
		t.Fatal("dead actor changed offer")
	}
	tradeAction(t, s, a, as, 18, nil)
	if s.tradeActive(a) || s.tradeActive(b) {
		t.Fatal("dead actor could not cancel provisional trade")
	}
}

type tradeSlowSink struct {
	started   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	packets   chan []byte
	once      sync.Once
	closeOnce sync.Once
}

func slowTradeSink() *tradeSlowSink {
	return &tradeSlowSink{started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}), packets: make(chan []byte, 128)}
}
func (s *tradeSlowSink) WritePacket(p []byte) error {
	s.once.Do(func() {
		close(s.started)
		select {
		case <-s.release:
		case <-s.closed:
		}
	})
	select {
	case <-s.closed:
		return errors.New("test recipient closed")
	default:
	}
	s.packets <- append([]byte(nil), p...)
	return nil
}
func (s *tradeSlowSink) Close() error { s.closeOnce.Do(func() { close(s.closed) }); return nil }
func tradeAwait(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}
func tradePacketAwait(t *testing.T, s *tradeSlowSink) []byte {
	t.Helper()
	select {
	case p := <-s.packets:
		return p
	case <-time.After(time.Second):
		t.Fatal("trade packet not delivered")
		return nil
	}
}

func TestTradeFIFOBlockedOpenCannotBeOvertakenByItemAndClose(t *testing.T) {
	s, a, _, _, _, _ := tradeFixture(t)
	blocked := slowTradeSink()
	defer blocked.Close()
	s.tradeEnter(a, blocked)
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	failed := s.trades.queueLocked([]tradePacket{{peer, []byte{10}}})
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	tradeAwait(t, blocked.started, "open write did not block")
	s.trades.mu.Lock()
	failed = s.trades.queueLocked([]tradePacket{{peer, []byte{0}}, {peer, []byte{18}}})
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	if !s.trades.mu.TryLock() {
		t.Fatal("blocked socket held trade authority mutex")
	}
	s.trades.mu.Unlock()
	close(blocked.release)
	for _, want := range []byte{10, 0, 18} {
		if got := tradePacketAwait(t, blocked); len(got) != 1 || got[0] != want {
			t.Fatalf("delivery%v want%d", got, want)
		}
	}
	s.tradeLeave(a)
	tradeAwait(t, peer.done, "departed peer writer did not exit")
}

func TestTradeSlowPeerQueueOverflowCancelsProvisionallyAndStopsWriter(t *testing.T) {
	s, a, b, _, bs, repo := tradeFixture(t)
	blocked := slowTradeSink()
	defer blocked.Close()
	s.tradeEnter(a, blocked)
	tradeAction(t, s, a, blocked, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAwait(t, blocked.started, "recipient did not block")
	tradeAction(t, s, a, blocked, 11, func(w *protocol.Writer) { w.I32(2) })
	ref := s.trades.rooms[1].reference
	tradeAction(t, s, b, bs, 9, func(w *protocol.Writer) { w.U32(ref); w.Zero(2) })
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	var out []tradePacket
	for i := 0; i < tradeOutputCapacity+1; i++ {
		out = append(out, tradePacket{peer, []byte{0}})
	}
	failed := s.trades.queueLocked(out)
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	tradeAwait(t, blocked.closed, "overflow did not close recipient")
	tradeAwait(t, peer.done, "closed recipient writer leaked")
	if s.tradeActive(a) || s.tradeActive(b) || repo.calls != 0 {
		t.Fatal("saturated peer retained a provisional room or committed assets")
	}
	if a.session.Character.Mesos != 1000 || b.session.Character.Mesos != 1000 {
		t.Fatal("queue failure moved provisional assets")
	}
}

func TestTradeReplacementInterruptsOldWriterAndOldFramesNeverReachNewSink(t *testing.T) {
	s, a, _, _, _, _ := tradeFixture(t)
	old := slowTradeSink()
	defer old.Close()
	s.tradeEnter(a, old)
	tradeAction(t, s, a, old, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAwait(t, old.started, "old write did not block")
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	s.trades.mu.Unlock()
	replacement := &liveCharacter{session: a.session}
	replacement.session.Generation++
	next := slowTradeSink()
	close(next.release)
	defer next.Close()
	s.tradeEnter(replacement, next)
	tradeAwait(t, old.closed, "replacement did not interrupt old recipient")
	tradeAwait(t, peer.done, "old replacement writer did not exit")
	tradeAction(t, s, replacement, next, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	packet := tradePacketAwait(t, next)
	if len(packet) < 3 || packet[2] != 10 {
		t.Fatalf("replacement got obsolete packet%x", packet)
	}
	s.tradeLeave(a)
	if !s.tradeActive(replacement) {
		t.Fatal("late old departure cancelled new room")
	}
}

func TestTradeSettlementDoesNotWaitForSlowPeerDelivery(t *testing.T) {
	s, a, b, as, _, repo := tradeFixture(t)
	slow := slowTradeSink()
	defer slow.Close()
	s.tradeEnter(b, slow)
	openTestTrade(t, s, a, b, as, slow)
	tradeAwait(t, slow.started, "peer did not block")
	finished := make(chan struct{})
	go func() {
		tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(0) })
		tradeAction(t, s, b, slow, 2, func(w *protocol.Writer) { w.U8(0) })
		close(finished)
	}()
	tradeAwait(t, finished, "slow recipient delayed durable settlement")
	if repo.calls != 1 || s.tradeActive(a) || s.tradeActive(b) {
		t.Fatal("confirmed settlement was lost behind socket writes")
	}
}
