package protocol

import (
	"encoding/binary"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
)

// Long damage carries lines above the v145 client's 32-bit fields. The client
// keeps an opaque token in the 32-bit line, whose encoder it cannot widen, and
// sends the value in a trailer appended to the attack frame. In the other
// direction 0x611 announces server tokens just before the frames that use them.
// Client map and wire format: Open-Maple docs/long-damage.md.
const (
	SendLongDamageMode   uint16 = 0x610
	SendLongDamageValues uint16 = 0x611

	// LongDamageMaxCap keeps every line exact in the client's double arithmetic
	// and within the 16 digits its damage renderer has positions for.
	LongDamageMaxCap int64 = 1<<53 - 1

	longDamageTokenFloor  uint32 = 0x7F000000
	longDamageServerFloor uint32 = 0x7F800000
	longDamageMagic              = "LDMG"
	longDamageLedgerSize         = 4096
)

var (
	longDamageCap  atomic.Int64
	longDamageNext atomic.Uint32
)

// Long damage is on unless MAPLEGO_LONG_DAMAGE=0.
func init() {
	if os.Getenv("MAPLEGO_LONG_DAMAGE") == "0" {
		return
	}
	limit := LongDamageMaxCap
	if text := os.Getenv("MAPLEGO_LONG_DAMAGE_CAP"); text != "" {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || value < 1 || value > LongDamageMaxCap {
			slog.Error("long_damage_disabled_by_invalid_cap", "value", text, "maximum", LongDamageMaxCap)
			return
		}
		limit = value
	}
	longDamageCap.Store(limit)
}

// LongDamageCap is the configured per-line cap, or 0 while the feature is off.
func LongDamageCap() int64 { return longDamageCap.Load() }

// SetLongDamageCap overrides the environment switch; tests restore 0 afterward.
func SetLongDamageCap(limit int64) { longDamageCap.Store(limit) }

// LongDamageModePacket arms the client after it enters a field.
func LongDamageModePacket(limit int64) []byte {
	w := NewWriter(SendLongDamageMode)
	w.U32(uint32(limit))
	w.U32(uint32(limit >> 32))
	return w.Packet()
}

// LongDamageLedger keeps one session's recent client values. It is owned by the
// session's read loop. Values stay until evicted because a skill can calculate
// its lines before an unrelated attack frame carries the trailer.
type LongDamageLedger struct {
	values map[uint32]int64
	order  []uint32
}

// Absorb moves a trailer from an attack frame into the ledger and returns the
// native frame, which every attack decoder and stored wire expects unchanged.
func (l *LongDamageLedger) Absorb(packet []byte) []byte {
	limit := LongDamageCap()
	if limit == 0 || len(packet) < 2+8 || string(packet[len(packet)-4:]) != longDamageMagic {
		return packet
	}
	switch binary.LittleEndian.Uint16(packet) {
	case RecvMeleeAttack, RecvShootAttack, RecvMagicAttack, RecvBodyAttack, RecvSummonAttack:
	default:
		return packet
	}
	count := int(binary.LittleEndian.Uint32(packet[len(packet)-8:]))
	if count < 1 || count > (len(packet)-2-8)/12 {
		return packet
	}
	end := len(packet) - 8
	start := end - count*12
	for at := start; at < end; at += 12 {
		token := binary.LittleEndian.Uint32(packet[at:])
		value := int64(binary.LittleEndian.Uint64(packet[at+4:]))
		if token >= longDamageTokenFloor && token < longDamageServerFloor && value > 0 && value <= limit {
			l.store(token, value)
		}
	}
	return packet[:start:start]
}

func (l *LongDamageLedger) store(token uint32, value int64) {
	if l.values == nil {
		l.values = make(map[uint32]int64)
	}
	if _, known := l.values[token]; !known {
		if len(l.order) == longDamageLedgerSize {
			delete(l.values, l.order[0])
			l.order = l.order[1:]
		}
		l.order = append(l.order, token)
	}
	l.values[token] = value
}

// Apply resolves token lines in decoded targets. A token with no announced
// value claims nothing: the 32-bit number is not damage.
func (l *LongDamageLedger) Apply(targets []AttackTarget) {
	if LongDamageCap() == 0 {
		return
	}
	for i := range targets {
		for j := range targets[i].Lines {
			line := &targets[i].Lines[j]
			if !line.Token() {
				continue
			}
			value, known := l.values[line.Raw&0x7fffffff]
			if !known {
				value = -1
			}
			line.Long = value
			line.Damage = max(int(value), 0)
		}
	}
}

// Token reports a line whose 32-bit value stands for a long damage value.
func (l AttackLine) Token() bool {
	low := l.Raw & 0x7fffffff
	return LongDamageCap() > 0 && low >= longDamageTokenFloor
}

// LongDamageOut collects the tokens of one published attack. The nil value is
// valid and saturates large values below the token range.
type LongDamageOut struct {
	w     *Writer
	count uint16
}

// NewLongDamageOut returns nil while the feature is off.
func NewLongDamageOut() *LongDamageOut {
	if LongDamageCap() == 0 {
		return nil
	}
	return &LongDamageOut{}
}

// Wire is the 32-bit line value for damage.
func (o *LongDamageOut) Wire(damage int) uint32 {
	if damage < int(longDamageTokenFloor) {
		return uint32(damage)
	}
	if o == nil {
		return longDamageTokenFloor - 1
	}
	token := longDamageServerFloor + longDamageNext.Add(1)%(0x80000000-longDamageServerFloor)
	if o.w == nil {
		o.w = NewWriter(SendLongDamageValues)
		o.w.U16(0)
	}
	o.w.U32(token)
	o.w.U32(uint32(damage))
	o.w.U32(uint32(int64(damage) >> 32))
	o.count++
	return token
}

// Packet is the 0x611 frame to write first, or nil when no value needs one.
func (o *LongDamageOut) Packet() []byte {
	if o == nil || o.w == nil {
		return nil
	}
	packet := o.w.Packet()
	binary.LittleEndian.PutUint16(packet[2:], o.count)
	return packet
}
