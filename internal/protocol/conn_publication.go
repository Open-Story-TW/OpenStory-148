package protocol

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// PacketPublication reserves one bounded position in the connection's complete
// outbound stream. Reserving never waits for a socket or an earlier writer.
// The owner must write once or abort. Abort fails the stream closed: silently
// skipping a committed asset snapshot would leave the client's state ambiguous.
type PacketPublication interface {
	WritePackets([][]byte) error
	Abort(error)
}

const maxPendingPublications = 128

var ErrPublicationQueueFull = errors.New("outbound publication queue full")
var ErrPublicationConsumed = errors.New("outbound publication already consumed")
var ErrPublicationTimeout = errors.New("outbound publication deadline exceeded")

type packetPublication struct {
	conn        *Conn
	previous    <-chan struct{}
	done        chan struct{}
	expires     time.Time
	used        atomic.Bool
	releaseOnce sync.Once
}

func (c *Conn) initOrderLocked() {
	if c.orderClosed == nil {
		c.orderClosed = make(chan struct{})
		c.orderTail = make(chan struct{})
		close(c.orderTail)
		c.orderSlots = make(map[*packetPublication]struct{})
	}
}
func (c *Conn) ReservePublication() (PacketPublication, error) {
	c.orderMu.Lock()
	c.initOrderLocked()
	select {
	case <-c.orderClosed:
		c.orderMu.Unlock()
		return nil, net.ErrClosed
	default:
	}
	if len(c.orderSlots) >= maxPendingPublications {
		c.orderMu.Unlock()
		return nil, ErrPublicationQueueFull
	}
	wait := c.writeTimeout
	if wait <= 0 || wait > 10*time.Second {
		wait = 10 * time.Second
	}
	p := &packetPublication{conn: c, previous: c.orderTail, done: make(chan struct{}), expires: time.Now().Add(wait)}
	c.orderTail = p.done
	c.orderSlots[p] = struct{}{}
	c.orderMu.Unlock()
	return p, nil
}
func (p *packetPublication) release() {
	p.releaseOnce.Do(func() { p.conn.orderMu.Lock(); delete(p.conn.orderSlots, p); close(p.done); p.conn.orderMu.Unlock() })
}
func (p *packetPublication) Abort(_ error) { p.used.Store(true); _ = p.conn.Close(); p.release() }
func (p *packetPublication) wait() error {
	remaining := time.Until(p.expires)
	if remaining <= 0 {
		_ = p.conn.Close()
		return ErrPublicationTimeout
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-p.conn.orderClosed:
		return net.ErrClosed
	case <-timer.C:
		_ = p.conn.Close()
		return ErrPublicationTimeout
	case <-p.previous:
	}
	select {
	case <-p.conn.orderClosed:
		return net.ErrClosed
	default:
		return nil
	}
}
func (p *packetPublication) WritePackets(packets [][]byte) (err error) {
	if !p.used.CompareAndSwap(false, true) {
		return ErrPublicationConsumed
	}
	defer p.release()
	if len(packets) == 0 || len(packets) > 64 {
		p.Abort(ErrMalformed)
		return fmt.Errorf("%w: publication batch size", ErrMalformed)
	}
	for _, packet := range packets {
		if len(packet) < 2 || len(packet) > MaxPacketSize {
			p.Abort(ErrMalformed)
			return fmt.Errorf("%w: publication packet length", ErrMalformed)
		}
	}
	if err = p.wait(); err != nil {
		return err
	}
	p.conn.writeMu.Lock()
	defer p.conn.writeMu.Unlock()
	select {
	case <-p.conn.orderClosed:
		return net.ErrClosed
	default:
	}
	defer func() {
		if err != nil {
			_ = p.conn.Close()
		}
	}()
	for _, packet := range packets {
		if time.Now().After(p.expires) {
			return ErrPublicationTimeout
		}
		if err = p.conn.writePacketOrdered(packet, p.expires); err != nil {
			return err
		}
	}
	return nil
}

// Close wakes reserved slots and ordinary writers immediately, including when
// ReadPacket discovers EOF before the world disconnect-save cleanup starts.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.orderMu.Lock()
		c.initOrderLocked()
		close(c.orderClosed)
		slots := make([]*packetPublication, 0, len(c.orderSlots))
		for p := range c.orderSlots {
			slots = append(slots, p)
		}
		c.orderMu.Unlock()
		if c.Conn != nil {
			c.closeErr = c.Conn.Close()
		}
		for _, p := range slots {
			p.release()
		}
	})
	return c.closeErr
}
