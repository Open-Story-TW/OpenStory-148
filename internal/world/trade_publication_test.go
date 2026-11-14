package world

import (
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/protocol"
)

type tradeGateConn struct {
	net.Conn
	armed                    atomic.Bool
	first                    sync.Once
	closeOnce                sync.Once
	started, release, closed chan struct{}
}

func (c *tradeGateConn) Write(p []byte) (int, error) {
	if c.armed.Load() {
		c.first.Do(func() {
			close(c.started)
			select {
			case <-c.release:
			case <-c.closed:
			}
		})
	}
	return c.Conn.Write(p)
}
func (c *tradeGateConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}
func tradeConnPair(t *testing.T) (*protocol.Conn, *protocol.Conn, *tradeGateConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type accepted struct {
		server *protocol.Conn
		gate   *tradeGateConn
		err    error
	}
	ready := make(chan accepted, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			ready <- accepted{err: err}
			return
		}
		gate := &tradeGateConn{Conn: raw, started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		c, err := protocol.NewServerConn(gate)
		ready <- accepted{c, gate, err}
	}()
	client, err := protocol.Dial(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	v := <-ready
	if v.err != nil {
		t.Fatal(v.err)
	}
	v.server.SetTimeouts(time.Second, time.Second)
	client.SetTimeouts(time.Second, time.Second)
	t.Cleanup(func() { _ = v.server.Close(); _ = client.Close() })
	v.gate.armed.Store(true)
	return v.server, client, v.gate
}

func TestTradeActualConnReservationPreventsStaleMesosOverwritingLaterDirectAsset(t *testing.T) {
	s, a, b, as, _, repo := tradeFixture(t)
	server, client, gate := tradeConnPair(t)
	s.tradeEnter(b, server)
	openTestTrade(t, s, a, b, as, server)
	tradeAwait(t, gate.started, "old trade write did not block")
	tradeAction(t, s, a, as, 1, func(w *protocol.Writer) { w.I32(100) })
	tradeAction(t, s, a, as, 2, func(w *protocol.Writer) { w.U8(0) })
	tradeAction(t, s, b, server, 2, func(w *protocol.Writer) { w.U8(0) })
	if repo.calls != 1 || b.session.Character.Mesos != 1100 || s.tradeActive(b) {
		t.Fatal("commit waited for socket or left wrong state")
	}
	// Model a subsequent durable asset operation whose packet is sent directly,
	// outside trade FIFO, after persistMu has been released.
	newer := make(chan error, 1)
	go func() { newer <- server.WritePacket(protocol.UpdateMesos(1099, true)) }()
	close(gate.release)
	oldIndex, newIndex := -1, -1
	for n := 0; n < 7; n++ {
		packet, err := client.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(packet, protocol.UpdateMesos(1100, true)) {
			oldIndex = n
		}
		if bytes.Equal(packet, protocol.UpdateMesos(1099, true)) {
			newIndex = n
		}
	}
	if oldIndex < 0 || newIndex <= oldIndex {
		t.Fatalf("new state was overtaken:old index%d,new%d", oldIndex, newIndex)
	}
	select {
	case err := <-newer:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("direct asset publication did not complete")
	}
}

func TestTradeReservedSlotInvalidGenerationAbortsBeforeSending(t *testing.T) {
	s, a, _, _, _, _ := tradeFixture(t)
	server, client, _ := tradeConnPair(t)
	s.tradeEnter(a, server)
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	failed := s.trades.queueLocked([]tradePacket{{peer, protocol.TradeConfirm()}})
	a.mu.Lock()
	a.session.Generation++
	a.mu.Unlock()
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	tradeAwait(t, peer.done, "invalid-generation writer retained its slot")
	if _, err := client.ReadPacket(); err == nil {
		t.Fatal("obsolete generation publication was delivered")
	}
	if slot, err := server.ReservePublication(); err == nil {
		slot.Abort(errTradeOutput)
		t.Fatal("invalid-generation abort left stream open")
	}
}

func TestTradeQueueOverflowAbortsUnqueuedActualConnReservation(t *testing.T) {
	s, a, _, _, _, _ := tradeFixture(t)
	server, client, gate := tradeConnPair(t)
	s.tradeEnter(a, server)
	tradeAction(t, s, a, server, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) })
	tradeAwait(t, gate.started, "publication did not reach blocked writer")
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	var out []tradePacket
	for n := 0; n < tradeOutputCapacity+1; n++ {
		out = append(out, tradePacket{peer, protocol.TradeConfirm()})
	}
	failed := s.trades.queueLocked(out)
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	tradeAwait(t, peer.done, "overflow leaked actual Conn reservation writer")
	if _, err := client.ReadPacket(); err == nil {
		t.Fatal("overflow left partial trade stream alive")
	}
	if slot, err := server.ReservePublication(); err == nil {
		slot.Abort(errTradeOutput)
		t.Fatal("unqueued overflow slot was not aborted")
	}
}

func TestTradeRetireDrainsReservedFramesBeforeNormalChannelRedirect(t *testing.T) {
	s, a, _, _, _, _ := tradeFixture(t)
	server, client, gate := tradeConnPair(t)
	s.tradeEnter(a, server)
	s.trades.mu.Lock()
	peer := s.trades.clients[1]
	failed := s.trades.queueLocked([]tradePacket{{peer, protocol.TradeConfirm()}})
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	tradeAwait(t, gate.started, "source publication did not block")
	s.tradeLeave(a) // normal source handoff: do not abort or prematurely close its socket
	redirect := protocol.ChannelChange("127.0.0.1", 7575)
	done := make(chan error, 1)
	go func() { done <- server.WritePacket(redirect) }()
	close(gate.release)
	for _, want := range [][]byte{protocol.TradeConfirm(), redirect} {
		packet, err := client.ReadPacket()
		if err != nil || !bytes.Equal(packet, want) {
			t.Fatalf("handoff ordering:%x %v", packet, err)
		}
	}
	tradeAwait(t, peer.done, "retired source writer did not finish draining")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("redirect blocked behind leaked trade slot")
	}
}
