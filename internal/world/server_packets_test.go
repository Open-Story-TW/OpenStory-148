package world

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

type packetTestRepository struct {
	domain.Repository
	session domain.CharacterSession
}

func (r packetTestRepository) ConsumeTransfer(context.Context, domain.TransferClaim) (domain.CharacterSession, error) {
	return r.session, nil
}
func (r packetTestRepository) ValidateSceneOwner(_ context.Context, v domain.CharacterSession) error {
	if !sameSceneOwner(r.session, v) {
		return domain.ErrStaleOwner
	}
	return nil
}
func (r packetTestRepository) ReleaseCharacter(context.Context, domain.CharacterSession) error {
	return nil
}

func TestUnsupportedGameplayUnlocksNativeRequestWithoutMutation(t *testing.T) {
	// Counts the stock entry frames; long damage adds its 0x610 frame.
	previousCap := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previousCap) })
	protocol.SetLongDamageCap(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := domain.CharacterSession{AccountID: 1, CharacterID: 1, SessionID: "packet-test", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, AccountID: 1, Name: "PacketTest", Level: 1, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000, Revision: 1}}
	s := &Server{repo: packetTestRepository{session: session}, online: make(map[int64]domain.CharacterSession), maps: newMapSpace(800), drops: newDropSpace()}
	s.ready.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, e := listener.Accept()
		if e == nil {
			s.handle(ctx, raw, 1)
		}
	}()
	c, err := protocol.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetTimeouts(time.Second, time.Second)
	w := protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(1)
	if err = c.WritePacket(w.Packet()); err != nil {
		t.Fatal(err)
	}
	for range 7 { // entry frames, including the client UI, fee tables and parcel time
		if _, err = c.ReadPacket(); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.WritePacket(protocol.NewWriter(0x7eee).Packet()); err != nil {
		t.Fatal(err)
	}
	packet, err := c.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 13 || binary.LittleEndian.Uint16(packet) != protocol.SendUpdateStats || packet[2] != 1 || binary.LittleEndian.Uint64(packet[3:11]) != 0 {
		t.Fatalf("native action gate not released: %x", packet)
	}
	s.mu.RLock()
	value := s.online[1].Character
	s.mu.RUnlock()
	if value.Mesos != 0 || value.Revision != 1 || value.HP != 50 {
		t.Fatalf("unsupported packet changed state: %+v", value)
	}
	c.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("connection cleanup stalled")
	}
}

type channelPacketRepository struct{ packetTestRepository }

func (r channelPacketRepository) BeginChannelTransfer(context.Context, domain.CharacterSession, int) error {
	return nil
}

func TestChannelRedirectWaitsForClientSocketClose(t *testing.T) {
	// Counts the stock entry frames; long damage adds its 0x610 frame.
	previousCap := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previousCap) })
	protocol.SetLongDamageCap(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := domain.CharacterSession{AccountID: 1, CharacterID: 1, SessionID: "channel-packet-test", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, AccountID: 1, Name: "ChannelTest", Level: 1, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000, Revision: 1}}
	s := &Server{repo: channelPacketRepository{packetTestRepository{session: session}}, online: make(map[int64]domain.CharacterSession), maps: newMapSpace(800), drops: newDropSpace(), wcfg: platform.WorldConfig{Host: "127.0.0.1", ChannelCount: 2, ChannelBasePort: 25000}}
	s.ready.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, e := listener.Accept()
		if e == nil {
			s.handle(ctx, raw, 1)
		}
	}()
	c, err := protocol.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetTimeouts(time.Second, time.Second)
	w := protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(1)
	if err = c.WritePacket(w.Packet()); err != nil {
		t.Fatal(err)
	}
	// SetField, the launcher interface switches, then the four packets that
	// finish entering a map.
	for range 8 { // entry frames, including the client UI, fee tables and parcel time
		if _, err = c.ReadPacket(); err != nil {
			t.Fatal(err)
		}
	}
	w = protocol.NewWriter(protocol.RecvChangeChannel)
	w.U8(1)
	w.U32(100)
	if err = c.WritePacket(w.Packet()); err != nil {
		t.Fatal(err)
	}
	packet, err := c.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 10 || binary.LittleEndian.Uint16(packet) != protocol.SendChannelChange {
		t.Fatalf("redirect=%x", packet)
	}
	select {
	case <-done:
		t.Fatal("source socket closed before client processed redirect")
	case <-time.After(30 * time.Millisecond):
	}
	c.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("redirect source did not close after client EOF")
	}
}
