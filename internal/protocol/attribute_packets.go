package protocol

import "maple-go-server/internal/domain"

const (
	RecvAttributeAllocate     uint16 = 0x9d
	RecvAttributeAutoAllocate uint16 = 0x9e
)

// CWvsContext sender 0x934B9D writes TickU32 + StatMaskU64. The retained Java
// reads only the low DWORD; consume and validate the full native packet.
func ReadAttributeAllocation(r *Reader) (domain.AttributeRequest, error) {
	req := domain.AttributeRequest{ClientTick: r.U32(), Stat: uint64(r.I64())}
	if r.Err() != nil || r.Remaining() != 0 {
		return req, ErrMalformed
	}
	return req, nil
}

// Native CWvsContext sender 0x934D10 writes a count followed by complete
// eight-byte stat masks and signed four-byte amounts (not Java's fixed pair).
func ReadAttributeAutoAllocation(r *Reader) (domain.AttributeRequest, error) {
	req := domain.AttributeRequest{ClientTick: r.U32()}
	count := r.U32()
	if r.Err() != nil || count < 1 || count > 4 || r.Remaining() != int(count)*12 {
		return req, ErrMalformed
	}
	for range count {
		req.Allocations = append(req.Allocations, domain.AttributeAllocation{Stat: uint64(r.I64()), Amount: r.I32()})
	}
	return req, r.Err()
}

func AttributeStats(c domain.Character) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x43c0)
	for _, stat := range []int{c.STR, c.DEX, c.INT, c.LUK, c.AP} {
		writeStat(w, stat)
	}
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
