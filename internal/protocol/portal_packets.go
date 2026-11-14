package protocol

// Java playerMessage(5) -> broadcastMsg5. Native context0x92B204 routes
// opcode0x54 toOnBroadcastMsg0x94444F: typeU8 at0x94446C, string at0x9444D4;
// case5 at0x945219 adds chat text without a modal or additional payload.
func PortalTrainingMessage(text string) []byte {
	w := NewWriter(0x58)
	w.U8(5)
	w.String(text)
	return w.Packet()
}
