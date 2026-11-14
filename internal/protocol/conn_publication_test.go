package protocol

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type gatedPublicationConn struct {
	net.Conn
	started   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	first     sync.Once
	closeOnce sync.Once
}

func (c *gatedPublicationConn) Write(b []byte) (int, error) {
	c.first.Do(func() {
		close(c.started)
		select {
		case <-c.release:
		case <-c.closed:
		}
	})
	return c.Conn.Write(b)
}
func (c *gatedPublicationConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}
func publicationPair(t *testing.T, gate bool) (*Conn, *Conn, *gatedPublicationConn) {
	t.Helper()
	a, b := net.Pipe()
	var g *gatedPublicationConn
	var serverNet net.Conn = a
	if gate {
		g = &gatedPublicationConn{Conn: a, started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		serverNet = g
	}
	iv := [4]byte{1, 2, 3, 4}
	other := [4]byte{5, 6, 7, 8}
	server := &Conn{Conn: serverNet, send: newAES(iv, ^Version), recv: newAES(other, Version), readTimeout: time.Second, writeTimeout: time.Second}
	client := &Conn{Conn: b, send: newAES(other, Version), recv: newAES(iv, ^Version), client: true, readTimeout: time.Second, writeTimeout: time.Second}
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return server, client, g
}
func awaitPublication(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("publication waiter leaked")
		return nil
	}
}
func publicationSlots(t *testing.T, c *Conn) int {
	t.Helper()
	c.orderMu.Lock()
	defer c.orderMu.Unlock()
	return len(c.orderSlots)
}

func TestPublicationReserveWhileOldWriteBlockedPreservesNewAssetOrder(t *testing.T) {
	server, client, gate := publicationPair(t, true)
	old := make(chan error, 1)
	go func() { old <- server.WritePacket([]byte{1, 0, 1}) }()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("old writer did not enter socket")
	}
	// Reserving under an authority mutex must not wait for the slow socket.
	reserved := make(chan PacketPublication, 1)
	go func() {
		p, err := server.ReservePublication()
		if err == nil {
			reserved <- p
		}
	}()
	var slot PacketPublication
	select {
	case slot = <-reserved:
	case <-time.After(time.Second):
		t.Fatal("reserve waited for old socket writer")
	}
	newer := make(chan error, 1)
	go func() { newer <- server.WritePacket(UpdateUseSlot(1, 4)) }()
	batch := make(chan error, 1)
	go func() { batch <- slot.WritePackets([][]byte{UpdateUseSlot(1, 5), UpdateMesos(100, true)}) }()
	close(gate.release)
	for _, want := range [][]byte{{1, 0, 1}, UpdateUseSlot(1, 5), UpdateMesos(100, true), UpdateUseSlot(1, 4)} {
		got, err := client.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("new asset state overtaken:got%x want%x", got, want)
		}
	}
	for _, done := range []chan error{old, batch, newer} {
		if err := awaitPublication(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if n := publicationSlots(t, server); n != 0 {
		t.Fatalf("released slots%d", n)
	}
}

func TestPublicationAbortUnsentSlotClosesStreamAndReleasesWaiters(t *testing.T) {
	server, client, _ := publicationPair(t, false)
	slot, err := server.ReservePublication()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.WritePacket(UpdateUseSlot(1, 4)) }()
	slot.Abort(errors.New("trade queue overflow before flush"))
	if awaitPublication(t, done) == nil {
		t.Fatal("writer continued after missing asset snapshot")
	}
	if _, err = client.ReadPacket(); err == nil {
		t.Fatal("aborted stream delivered a later asset snapshot")
	}
	if n := publicationSlots(t, server); n != 0 {
		t.Fatalf("abandoned slots leaked%d", n)
	}
	slot.Abort(errors.New("duplicate abort"))
	if err = slot.WritePackets([][]byte{{1, 0}}); !errors.Is(err, ErrPublicationConsumed) {
		t.Fatalf("aborted slot reused:%v", err)
	}
}

func TestPublicationTimeoutAndQuotaFailClosed(t *testing.T) {
	t.Run("unwritten reservation deadline", func(t *testing.T) {
		server, _, _ := publicationPair(t, false)
		server.writeTimeout = 20 * time.Millisecond
		if _, err := server.ReservePublication(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- server.WritePacket([]byte{1, 0}) }()
		if awaitPublication(t, done) == nil {
			t.Fatal("timed-out reservation did not fail stream")
		}
		if n := publicationSlots(t, server); n != 0 {
			t.Fatalf("timeout leaked%dslots", n)
		}
	})
	t.Run("ignored normal write queuefull still closes", func(t *testing.T) {
		server, client, _ := publicationPair(t, false)
		var slots []PacketPublication
		for n := 0; n < maxPendingPublications; n++ {
			slot, err := server.ReservePublication()
			if err != nil {
				t.Fatal(err)
			}
			slots = append(slots, slot)
		}
		if err := server.WritePacket([]byte{1, 0}); !errors.Is(err, ErrPublicationQueueFull) {
			t.Fatalf("quota:%v", err)
		}
		if _, err := client.ReadPacket(); err == nil {
			t.Fatal("queuefull left ambiguous stream alive")
		}
		if n := publicationSlots(t, server); n != 0 {
			t.Fatalf("queuefull leaked%dslots", n)
		}
		for _, slot := range slots {
			slot.Abort(errors.New("cleanup"))
		}
	})
}

func TestPublicationCloseDuringFlushAndWaitIsIdempotent(t *testing.T) {
	server, _, gate := publicationPair(t, true)
	first, err := server.ReservePublication()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 33)
	go func() { done <- first.WritePackets([][]byte{{1, 0}, {2, 0}}) }()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("flush did not begin")
	}
	for n := 0; n < 32; n++ {
		go func() { done <- server.WritePacket([]byte{3, 0}) }()
	}
	closed := make(chan struct{})
	go func() { _ = server.Close(); close(closed) }()
	_ = server.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("concurrent close deadlocked")
	}
	for n := 0; n < 33; n++ {
		if awaitPublication(t, done) == nil {
			t.Fatal("closed stream writer reported success")
		}
	}
	if n := publicationSlots(t, server); n != 0 {
		t.Fatalf("concurrent close leaked%dslots", n)
	}
}

func TestPublicationReadEOFReleasesQueuedAssetWriterBeforeCleanup(t *testing.T) {
	server, client, _ := publicationPair(t, false)
	if _, err := server.ReservePublication(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.WritePacket([]byte{1, 0}) }()
	_ = client.Close()
	if _, err := server.ReadPacket(); err == nil {
		t.Fatal("expected remote EOF")
	}
	if awaitPublication(t, done) == nil {
		t.Fatal("queued writer continued after read EOF")
	}
	if n := publicationSlots(t, server); n != 0 {
		t.Fatalf("EOF leaked%dslots", n)
	}
}
