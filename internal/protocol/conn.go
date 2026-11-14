package protocol

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const Version uint16 = 148
const Locale byte = 6
const MaxPacketSize = 65535

// Conn is one independently framed v145 stream; read and write may run
// concurrently, but callers must serialize their packet-handling state machine.
type Conn struct {
	net.Conn
	send, recv                *aesOFB
	writeMu                   sync.Mutex
	readTimeout, writeTimeout time.Duration
	client                    bool
	orderMu                   sync.Mutex
	orderTail                 chan struct{}
	orderClosed               chan struct{}
	orderSlots                map[*packetPublication]struct{}
	closeOnce                 sync.Once
	closeErr                  error
	// trace, when set, sees every outbound plaintext packet (acceptance
	// diagnostics for one character; MAPLEGO_TRACE_CHARACTERS).
	trace atomic.Pointer[func([]byte)]
	// secure is set once, before the Conn is shared, when the launcher key
	// exchange succeeded; its cipher and counter are then guarded by writeMu
	// on a server Conn and by the single reader on a client Conn.
	secure *secureSession
	// pending is a legacy client's first header, read while looking for an
	// upgrade request.
	pending *[4]byte
}

// Secure reports whether the launcher key exchange ran on this connection.
func (c *Conn) Secure() bool { return c.secure != nil }

// SetTrace installs an outbound packet observer (nil removes it).
func (c *Conn) SetTrace(f func([]byte)) {
	if f == nil {
		c.trace.Store(nil)
		return
	}
	c.trace.Store(&f)
}

func NewServerConn(raw net.Conn) (*Conn, error) {
	key, err := currentLauncherKey()
	if err != nil {
		return nil, err
	}
	var private ed25519.PrivateKey
	required := false
	if key != nil {
		private, required = key.private, key.required
	}
	c, err := newServerConn(raw, private, required)
	if errors.Is(err, ErrLauncherKeyRequired) {
		// The only trace of an old or foreign client being turned away.
		slog.Warn("launcher_key_refused", "remote", raw.RemoteAddr().String())
	}
	return c, err
}

func newServerConn(raw net.Conn, key ed25519.PrivateKey, required bool) (*Conn, error) {
	var recvIV, sendIV [4]byte
	var magic [4]byte
	copy(magic[:], secureMagic)
	for {
		if _, err := rand.Read(recvIV[:]); err != nil {
			return nil, err
		}
		// A legacy client's first header must never look like the request.
		if !newAES(recvIV, Version).validHeader(magic) {
			break
		}
	}
	if _, err := rand.Read(sendIV[:]); err != nil {
		return nil, err
	}
	w := NewRawWriter()
	w.U16(14)
	w.U16(Version)
	w.String("1")
	w.Bytes(recvIV[:])
	w.Bytes(sendIV[:])
	w.U8(Locale)
	plain := w.Packet()
	hello := append([]byte(nil), plain...)
	raw.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := writeAll(raw, hello); err != nil {
		return nil, err
	}
	raw.SetWriteDeadline(time.Time{})
	c := &Conn{Conn: raw, send: newAES(sendIV, ^Version), recv: newAES(recvIV, Version), readTimeout: 2 * time.Minute, writeTimeout: 10 * time.Second}
	if key == nil {
		return c, nil
	}
	// The client speaks next either way: a native packet or the request.
	raw.SetReadDeadline(time.Now().Add(c.readTimeout))
	var first [4]byte
	if _, err := io.ReadFull(raw, first[:]); err != nil {
		return nil, err
	}
	if first != magic {
		if required {
			return nil, ErrLauncherKeyRequired
		}
		c.pending = &first
		return c, nil
	}
	request := make([]byte, secureRequestLen)
	copy(request, first[:])
	raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(raw, request[4:]); err != nil {
		return nil, err
	}
	raw.SetReadDeadline(time.Time{})
	response, session, err := answerSecureRequest(key, plain, request)
	if err != nil {
		return nil, err
	}
	// The hello is repeated inside the encrypted stream: the launcher consumed
	// the first copy itself and the game still has to read one.
	again := append([]byte(nil), plain...)
	session.stream.XORKeyStream(again, again)
	raw.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := writeAll(raw, append(response, again...)); err != nil {
		return nil, err
	}
	raw.SetWriteDeadline(time.Time{})
	c.secure = session
	return c, nil
}

// Dial uses the client perspective and is intended for real TCP integration tests.
func Dial(ctx context.Context, address string) (*Conn, error) {
	return dial(ctx, address, nil)
}

// DialSecure is Dial plus the launcher key exchange against server's key.
func DialSecure(ctx context.Context, address string, server ed25519.PublicKey) (*Conn, error) {
	if len(server) != ed25519.PublicKeySize {
		return nil, errors.New("server key must be an Ed25519 public key")
	}
	return dial(ctx, address, server)
}

func readHello(raw net.Conn, decrypt func([]byte)) (plain []byte, recvIV, sendIV [4]byte, err error) {
	plain = make([]byte, 16)
	if _, err = io.ReadFull(raw, plain); err != nil {
		return
	}
	decrypt(plain)
	hello := plain[2:]
	if binary.LittleEndian.Uint16(plain[:2]) != 14 {
		err = fmt.Errorf("%w: handshake length", ErrMalformed)
		return
	}
	if binary.LittleEndian.Uint16(hello[:2]) != Version || binary.LittleEndian.Uint16(hello[2:4]) != 1 || hello[4] != '1' || hello[13] != Locale {
		err = fmt.Errorf("%w: unsupported handshake", ErrMalformed)
		return
	}
	copy(sendIV[:], hello[5:9])
	copy(recvIV[:], hello[9:13])
	return
}

func dial(ctx context.Context, address string, server ed25519.PublicKey) (*Conn, error) {
	raw, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Conn, error) { raw.Close(); return nil, e }
	raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	plain, recvIV, sendIV, err := readHello(raw, func([]byte) {})
	if err != nil {
		return fail(err)
	}
	var session *secureSession
	if server != nil {
		pending, request, err := newSecureRequest(plain)
		if err != nil {
			return fail(err)
		}
		raw.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err = writeAll(raw, request); err != nil {
			return fail(err)
		}
		raw.SetWriteDeadline(time.Time{})
		response := make([]byte, 32+ed25519.SignatureSize)
		if _, err = io.ReadFull(raw, response); err != nil {
			return fail(err)
		}
		if session, err = pending.finish(server, response); err != nil {
			return fail(err)
		}
		again, _, _, err := readHello(raw, func(p []byte) { session.stream.XORKeyStream(p, p) })
		if err != nil {
			return fail(err)
		}
		if string(again) != string(plain) {
			return fail(fmt.Errorf("%w: repeated handshake differs", ErrMalformed))
		}
	}
	raw.SetReadDeadline(time.Time{})
	return &Conn{Conn: raw, send: newAES(sendIV, Version), recv: newAES(recvIV, ^Version), readTimeout: 10 * time.Second, writeTimeout: 10 * time.Second, client: true, secure: session}, nil
}
func (c *Conn) SetTimeouts(read, write time.Duration) { c.readTimeout = read; c.writeTimeout = write }
func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
func (c *Conn) ReadPacket() (packet []byte, err error) {
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	if c.readTimeout > 0 {
		c.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	var h [4]byte
	if c.pending != nil {
		h, c.pending = *c.pending, nil
	} else if _, err := io.ReadFull(c.Conn, h[:]); err != nil {
		return nil, err
	}
	if c.client {
		c.downlink(h[:])
	}
	if !c.recv.validHeader(h) {
		return nil, fmt.Errorf("%w: encrypted header", ErrMalformed)
	}
	size := packetLength(h)
	if size < 2 || size > MaxPacketSize {
		return nil, fmt.Errorf("%w: packet length", ErrMalformed)
	}
	p := make([]byte, size)
	if _, err := io.ReadFull(c.Conn, p); err != nil {
		return nil, err
	}
	if c.client {
		c.downlink(p)
	}
	c.recv.crypt(p)
	if c.client && c.secure != nil && launcherPacket(p) {
		if len(p) < 2+secureTagSize {
			return nil, fmt.Errorf("%w: launcher packet tag", ErrMalformed)
		}
		body := p[:len(p)-secureTagSize]
		want := c.secure.tag(body)
		if !hmac.Equal(want[:], p[len(body):]) {
			return nil, fmt.Errorf("%w: launcher packet tag", ErrMalformed)
		}
		p = body
	}
	return p, nil
}

// downlink applies the server-to-client stream layer in place; a connection
// without a verified session sends its bytes as they are.
func (c *Conn) downlink(p []byte) {
	if c.secure != nil {
		c.secure.stream.XORKeyStream(p, p)
	}
}
func (c *Conn) WritePacket(packet []byte) error {
	if len(packet) < 2 || len(packet) > MaxPacketSize {
		return fmt.Errorf("%w: outbound length", ErrMalformed)
	}
	publication, err := c.ReservePublication()
	if err != nil {
		if errors.Is(err, ErrPublicationQueueFull) {
			_ = c.Close()
		}
		return err
	}
	return publication.WritePackets([][]byte{packet})
}

// Caller owns the ordered publication slot and writeMu. This helper never
// recursively reserves another slot, including for multi-packet batches.
func (c *Conn) writePacketOrdered(packet []byte, deadline time.Time) error {
	c.SetWriteDeadline(deadline)
	p := append([]byte(nil), packet...)
	if f := c.trace.Load(); f != nil {
		(*f)(packet)
	}
	if !c.client && c.secure != nil && launcherPacket(p) {
		if len(p)+secureTagSize > MaxPacketSize {
			return fmt.Errorf("%w: outbound length", ErrMalformed)
		}
		tag := c.secure.tag(p)
		p = append(p, tag[:]...)
	}
	h := c.send.header(len(p))
	c.send.crypt(p)
	if !c.client {
		c.downlink(h[:])
		c.downlink(p)
	}
	if err := writeAll(c.Conn, h[:]); err != nil {
		return err
	}
	return writeAll(c.Conn, p)
}
