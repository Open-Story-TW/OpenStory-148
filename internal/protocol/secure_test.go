package protocol

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"
)

func testLauncherKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return ed25519.NewKeyFromSeed(seed)
}

// serveOnce accepts one connection with the given key policy and hands the
// server Conn (or the handshake error) to the test.
func serveOnce(t *testing.T, key ed25519.PrivateKey, required bool) (string, <-chan *Conn, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	conns, errs := make(chan *Conn, 1), make(chan error, 1)
	go func() {
		raw, err := l.Accept()
		if err != nil {
			errs <- err
			return
		}
		c, err := newServerConn(raw, key, required)
		if err != nil {
			raw.Close()
			errs <- err
			return
		}
		conns <- c
	}()
	return l.Addr().String(), conns, errs
}

func TestSecureRoundTrip(t *testing.T) {
	key := testLauncherKey()
	addr, conns, errs := serveOnce(t, key, true)
	client, err := DialSecure(context.Background(), addr, key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var server *Conn
	select {
	case server = <-conns:
	case err := <-errs:
		t.Fatal(err)
	}
	defer server.Close()
	if !server.Secure() || !client.Secure() {
		t.Fatal("exchange did not secure both ends")
	}
	native := NewWriter(0x0123)
	native.U32(7)
	config := ClientConfigPacket()
	ui := ClientUIPacket()
	for _, p := range [][]byte{native.Packet(), config, ui, native.Packet(), config} {
		if err := server.WritePacket(p); err != nil {
			t.Fatal(err)
		}
		got, err := client.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("got %x want %x", got, p)
		}
	}
	up := NewWriter(0x0045)
	up.U16(9)
	if err := client.WritePacket(up.Packet()); err != nil {
		t.Fatal(err)
	}
	if got, err := server.ReadPacket(); err != nil || !bytes.Equal(got, up.Packet()) {
		t.Fatalf("uplink got %x err %v", got, err)
	}
}

func TestSecureRejectsOtherServerKey(t *testing.T) {
	addr, _, _ := serveOnce(t, testLauncherKey(), false)
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := DialSecure(context.Background(), addr, other); err == nil {
		t.Fatal("a server with a different key was accepted")
	}
}

func TestLegacyClientKeepsWorkingUntilRequired(t *testing.T) {
	addr, conns, errs := serveOnce(t, testLauncherKey(), false)
	client, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	up := NewWriter(0x0045)
	up.U16(9)
	if err := client.WritePacket(up.Packet()); err != nil {
		t.Fatal(err)
	}
	var server *Conn
	select {
	case server = <-conns:
	case err := <-errs:
		t.Fatal(err)
	}
	defer server.Close()
	if server.Secure() {
		t.Fatal("legacy client was marked secure")
	}
	if got, err := server.ReadPacket(); err != nil || !bytes.Equal(got, up.Packet()) {
		t.Fatalf("first packet got %x err %v", got, err)
	}
	config := ClientConfigPacket()
	if err := server.WritePacket(config); err != nil {
		t.Fatal(err)
	}
	if got, err := client.ReadPacket(); err != nil || !bytes.Equal(got, config) {
		t.Fatalf("legacy downlink got %x err %v", got, err)
	}
}

func TestLegacyClientRefusedWhenRequired(t *testing.T) {
	addr, _, errs := serveOnce(t, testLauncherKey(), true)
	client, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.WritePacket(NewWriter(0x0045).Packet()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		if !errors.Is(err, ErrLauncherKeyRequired) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy client was not refused")
	}
}

// A tag is bound to its position: the same packet replayed or reordered on
// the connection no longer verifies.
func TestSecureTagIsBoundToPosition(t *testing.T) {
	s, err := deriveSecureSession(bytes.Repeat([]byte{9}, 32), []byte("hello"), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	packet := ClientUIPacket()
	if first, second := s.tag(packet), s.tag(packet); first == second {
		t.Fatal("tag does not depend on the packet's position")
	}
}

// Shared with the launcher: the launcher's secure channel test vectors.
func TestSecureVectors(t *testing.T) {
	key := testLauncherKey()
	hello := mustHex(t, "0e009400010031a1a2a3a4b1b2b3b406")
	clientKey := mustHex(t, "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	serverKey := mustHex(t, "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f")
	shared := mustHex(t, "4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742")
	if got := hex.EncodeToString(key.Public().(ed25519.PublicKey)); got != "79b5562e8fe654f94078b112e8a98ba7901f853ae695bed7e0e3910bad049664" {
		t.Fatalf("public key %s", got)
	}
	signature := ed25519.Sign(key, secureTranscript(hello, clientKey, serverKey))
	if got := hex.EncodeToString(signature); got != vectorSignature {
		t.Fatalf("signature %s", got)
	}
	s, err := deriveSecureSession(shared, hello, clientKey, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	stream := make([]byte, 24)
	s.stream.XORKeyStream(stream, stream)
	if got := hex.EncodeToString(stream); got != vectorStream {
		t.Fatalf("stream %s", got)
	}
	packet := mustHex(t, "150603000000")
	first, second := s.tag(packet), s.tag(packet)
	if got := hex.EncodeToString(first[:]) + hex.EncodeToString(second[:]); got != vectorTags {
		t.Fatalf("tags %s", got)
	}
}

const (
	vectorSignature = "131d9dd538e48fb40905d677abd98a85f691eb84fc140ea902502fa4142043017909649bbe439d21930f2228db53c150437b248b42451a54396050a5a2dafc0f"
	vectorStream    = "49fd03110fdca92db9d70793269cc75a202d57dace8a06c3"
	vectorTags      = "616ddf46d0cff1575f88d4389a0975d881a8259741aa8a7368ee7e121dfa489e"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
