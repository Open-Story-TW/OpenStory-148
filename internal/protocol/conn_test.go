package protocol

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestEncryptedTCPFragmentationAndIVAdvance(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer raw.Close()
		server, e := NewServerConn(raw)
		if e != nil {
			done <- e
			return
		}
		server.SetTimeouts(3*time.Second, 3*time.Second)
		for _, size := range []int{7, 1457, 65535} {
			p, e := server.ReadPacket()
			if e != nil {
				done <- e
				return
			}
			if len(p) != size {
				done <- ErrMalformed
				return
			}
			if e = server.WritePacket(p); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	client, err := Dial(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, size := range []int{7, 1457, 65535} {
		plain := make([]byte, size)
		for i := range plain {
			plain[i] = byte(i*13 + size)
		}
		p := append([]byte(nil), plain...)
		h := client.send.header(size)
		client.send.crypt(p)
		wire := append(h[:], p...)
		for offset := 0; offset < len(wire); {
			next := offset + 137
			if next > len(wire) {
				next = len(wire)
			}
			if err := writeAll(client.Conn, wire[offset:next]); err != nil {
				t.Fatal(err)
			}
			offset = next
		}
		reply, err := client.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(reply, plain) {
			t.Fatalf("roundtrip mismatch at size%d", size)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInvalidHeaderRejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer raw.Close()
		server, e := NewServerConn(raw)
		if e != nil {
			done <- e
			return
		}
		_, e = server.ReadPacket()
		done <- e
	}()
	client, err := Dial(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	h := client.send.header(2)
	h[0] ^= 1
	if err := writeAll(client.Conn, h[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("wanted malformed got%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server waited for malformedbody")
	}
}
