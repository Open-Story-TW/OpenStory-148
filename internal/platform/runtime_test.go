package platform

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestClosingOneRuntimeGroupLeavesOtherListenerAndSessionAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	one, two := NewTCPGroup(), NewTCPGroup()
	defer two.Close()
	echo := func(_ context.Context, c net.Conn) { _, _ = io.Copy(c, c) }
	for _, group := range []*TCPGroup{one, two} {
		if err := group.Listen(ctx, "127.0.0.1:0", echo); err != nil {
			t.Fatal(err)
		}
	}
	connect := func(group *TCPGroup) net.Conn {
		conn, err := net.DialTimeout("tcp", group.listeners[0].Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		return conn
	}
	c1, c2 := connect(one), connect(two)
	defer c1.Close()
	defer c2.Close()
	exchange := func(c net.Conn) {
		if _, err := c.Write([]byte{0x14, 0x91}); err != nil {
			t.Fatal(err)
		}
		var received [2]byte
		if _, err := io.ReadFull(c, received[:]); err != nil || received != [2]byte{0x14, 0x91} {
			t.Fatalf("exchange failed: %x %v", received, err)
		}
	}
	exchange(c1)
	exchange(c2)
	one.Close()
	var b [1]byte
	if _, err := c1.Read(b[:]); err == nil {
		t.Fatal("closed runtime left its session open")
	}
	exchange(c2)
	c3 := connect(two)
	defer c3.Close()
	exchange(c3)
}
