package world

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type tradeLostCommitRepository struct {
	*tradeTestRepository
	committed  bool
	resolveErr error
	resolved   int
	result     domain.TradeResult
	resolution domain.TradeSettlement
}

func (r *tradeLostCommitRepository) SettleTrade(ctx context.Context, q domain.TradeSettlement) (domain.TradeResult, error) {
	if r.committed {
		r.result, _ = r.tradeTestRepository.SettleTrade(ctx, q)
	} else {
		r.mu.Lock()
		r.calls++
		r.request = q
		r.mu.Unlock()
	}
	return domain.TradeResult{}, io.ErrUnexpectedEOF
}
func (r *tradeLostCommitRepository) ResolveTradeReceipt(_ context.Context, q domain.TradeSettlement) (domain.TradeResult, bool, error) {
	r.resolved++
	r.resolution = q
	return r.result, r.committed, r.resolveErr
}
func TestTradeLostCommitResponseResolvesSameOperationWithoutRepeatingGrant(t *testing.T) {
	s, a, b, as, bs, base := tradeFixture(t)
	repo := &tradeLostCommitRepository{tradeTestRepository: base, committed: true}
	s.repo = repo
	openTestTrade(t, s, a, b, as, bs)
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(100) })
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(0) })
	tradeAction(t, s, b, bs, 2, func(w *protocol.Writer) { w.U8(0) })
	if base.calls != 1 || repo.resolved != 1 || repo.resolution.ID != base.request.ID || repo.resolution.First.SessionID != base.request.First.SessionID || repo.resolution.FirstOffer.Mesos != 100 {
		t.Fatal("unknown commit was not reconciled with its exact original operation")
	}
	if a.session.Character.Mesos != 900 || b.session.Character.Mesos != 1100 || s.tradeActive(a) || s.tradeActive(b) {
		t.Fatal("committed receipt was reported as rollback or not merged")
	}
}
func TestTradeUnresolvedCommitDoesNotPublishFailureOrPretendAssetsUnchanged(t *testing.T) {
	s, a, b, as, bs, base := tradeFixture(t)
	repo := &tradeLostCommitRepository{tradeTestRepository: base, committed: true, resolveErr: errors.New("receipt unavailable")}
	s.repo = repo
	openTestTrade(t, s, a, b, as, bs)
	s.trades.mu.Lock()
	firstPeer, secondPeer := s.trades.clients[1], s.trades.clients[2]
	s.trades.mu.Unlock()
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(100) })
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(0) })
	err := s.handleTrade(context.Background(), b, bs, protocol.NewReader([]byte{2, 0}))
	if err == nil {
		t.Fatal("unknown commit returned ordinary success/failure")
	}
	if base.calls != 1 || repo.resolved != 1 {
		t.Fatal("unknown operation retried or was not resolved")
	}
	if s.tradeActive(a) || s.tradeActive(b) {
		t.Fatal("unknown disconnected room retained")
	}
	tradeAwait(t, firstPeer.done, "unresolved first peer writer did not stop")
	tradeAwait(t, secondPeer.done, "unresolved second peer writer did not stop")
	for _, sink := range []*memSink{as, bs} {
		sink.mu.Lock()
		bad := false
		for _, p := range sink.pkts {
			if len(p) >= 5 && p[0] == 0x4b && p[1] == 2 && p[2] == 18 && p[4] == 8 {
				bad = true
			}
		}
		sink.mu.Unlock()
		if bad {
			t.Fatal("possibly committed operation announced definite failure8")
		}
	}
}
func TestTradeAuthoritativeAbsentReceiptMayPublishOrdinaryFailure(t *testing.T) {
	s, a, b, as, bs, base := tradeFixture(t)
	repo := &tradeLostCommitRepository{tradeTestRepository: base}
	s.repo = repo
	openTestTrade(t, s, a, b, as, bs)
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(0) })
	tradeAction(t, s, b, bs, 2, func(w *protocol.Writer) { w.U8(0) })
	if repo.calls != 1 || repo.resolved != 1 || a.session.Character.Mesos != 1000 || b.session.Character.Mesos != 1000 {
		t.Fatal("absent receipt retried a grant or changed assets")
	}
	deadline := time.Now().Add(time.Second)
	found := false
	for time.Now().Before(deadline) {
		as.mu.Lock()
		for _, p := range as.pkts {
			found = found || len(p) >= 5 && p[0] == 0x4b && p[1] == 2 && p[2] == 18 && p[4] == 8
		}
		as.mu.Unlock()
		if found {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !found {
		t.Fatal("known rollback did not produce ordinary failure")
	}
}
func TestTradeBusyCandidateAndBusyNativeDeclineDoNotLeakRoom(t *testing.T) {
	for _, kind := range []string{"storage", "shop", "npc"} {
		t.Run(kind, func(t *testing.T) {
			s, a, b, as, bs, repo := tradeFixture(t)
			s.economyDialogs = newEconomyDialogSpace()
			busy := func() {
				if kind == "npc" {
					s.scripts = &scriptWorld{active: map[int64]*worldScript{2: {live: b, identity: scripting.Identity{CharacterID: 2, SessionID: b.session.SessionID, Generation: b.session.Generation}}}}
				} else {
					s.setEconomyDialog(b, &economyDialog{live: b, kind: kind})
				}
			}
			busy()
			tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
			tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
			if s.tradeActive(b) {
				t.Fatal("busy target reserved a pending trade")
			}
			tradeAction(t, s, a, as, 18, nil)
			s.economyDialogs = newEconomyDialogSpace()
			s.scripts = nil
			tradeAction(t, s, a, as, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
			tradeAction(t, s, a, as, 11, func(w *protocol.Writer) { w.I32(2) })
			ref := s.trades.rooms[1].reference
			busy()
			tradeAction(t, s, b, bs, 12, func(w *protocol.Writer) { w.U32(ref); w.U8(4) })
			if s.tradeActive(a) || s.tradeActive(b) || repo.calls != 0 {
				t.Fatal("native busy auto-decline was blocked by dialog gate")
			}
		})
	}
}
