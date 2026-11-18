package protocol

import (
	"crypto/sha256"
	"encoding/binary"
)

const SendCriticalDisplay uint16 = 0x619

const (
	criticalDisplayVersion = 1
	criticalDisplayTrailer = 84
	criticalDisplayLines   = 15 * 15
)

// CriticalDisplayOffer carries a nonce for this world visit. A supported
// launcher acknowledges it by using the nonce on a native attack trailer.
func CriticalDisplayOffer(nonce uint64) []byte {
	w := NewWriter(SendCriticalDisplay)
	w.U8(criticalDisplayVersion)
	w.U32(uint32(nonce))
	w.U32(uint32(nonce >> 32))
	return w.Packet()
}

// CriticalDisplayFrame is cosmetic input, separate from the native wire bit
// that the server uses when it computes damage bounds.
type CriticalDisplayFrame struct {
	nonce uint64
	count int
	bits  [32]byte
}

// SplitCriticalDisplay preserves the original wire body. It drops a framed
// but invalid trailer on an opted-in connection, without using its claims.
// LongDamageLedger.Absorb runs first, as LDMG is appended last by the launcher.
func SplitCriticalDisplay(packet []byte, nonce uint64) ([]byte, CriticalDisplayFrame) {
	var frame CriticalDisplayFrame
	if nonce == 0 || len(packet) < 2+criticalDisplayTrailer {
		return packet, frame
	}
	switch binary.LittleEndian.Uint16(packet) {
	case RecvMeleeAttack, RecvShootAttack, RecvMagicAttack, RecvBodyAttack:
	default:
		return packet, frame
	}
	start := len(packet) - criticalDisplayTrailer
	trailer := packet[start:]
	if string(trailer[:4]) != "OMCD" || string(trailer[80:]) != "CRIT" {
		return packet, frame
	}
	original := packet[:start:start]
	count := int(trailer[45])
	if binary.LittleEndian.Uint64(trailer[4:12]) != nonce || trailer[44] != criticalDisplayVersion ||
		trailer[46] != 0 || trailer[47] != 0 || count < 1 || count > criticalDisplayLines {
		return original, frame
	}
	digest := sha256.Sum256(original)
	if [32]byte(trailer[12:44]) != digest {
		return original, frame
	}
	bits := [32]byte(trailer[48:80])
	for index := count; index < len(bits)*8; index++ {
		if bits[index/8]&(1<<uint(index%8)) != 0 {
			return original, frame
		}
	}
	return original, CriticalDisplayFrame{nonce: nonce, count: count, bits: bits}
}

// Apply annotates a complete frame or leaves every line untouched.
func (f CriticalDisplayFrame) Apply(targets []AttackTarget, nonce uint64) {
	if f.count == 0 || f.nonce != nonce {
		return
	}
	n := 0
	for _, target := range targets {
		n += len(target.Lines)
	}
	if f.count != n {
		return
	}
	index := 0
	for i := range targets {
		for j := range targets[i].Lines {
			l := &targets[i].Lines[j]
			l.visualCriticality = displayOrdinary
			if f.bits[index/8]&(1<<uint(index%8)) != 0 {
				l.visualCriticality = displayCritical
			}
			index++
		}
	}
}
