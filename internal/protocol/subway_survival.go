package protocol

import "strconv"

// CField_Massacre (v148 0x5314E0) consumes the offset for its own clock-driven gauge.
func SubwayGauge(offset int) []byte {
	w := NewWriter(0xEE)
	w.I32(int32(offset))
	return w.Packet()
}

// CField_MassacreResult (v148 0x533967) consumes rank and bonus EXP.
func SubwayResult(rank byte, exp int) []byte {
	w := NewWriter(0xEF)
	w.U8(rank)
	w.I32(int32(exp))
	return w.Packet()
}

// Session value dispatch is v148 CWvsContext::OnPacket 0x70 -> 0x9C2CAC.
func SubwayCounter(name string, value int) []byte {
	w := NewWriter(0x70)
	w.String(name)
	w.String(strconv.Itoa(value))
	return w.Packet()
}
