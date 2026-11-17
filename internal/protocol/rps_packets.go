package protocol

// The v148 dialog sends 0xEB with a mode byte. Only the choice request
// appends another byte (0x69697C). The server reply uses 0x249 (0x6984FB).
const (
	RecvRPS uint16 = 0xEB
	sendRPS uint16 = 0x249

	RPSStart   byte = 0
	RPSChoose  byte = 1
	RPSTimeout byte = 2
	RPSNext    byte = 3
	RPSLeave   byte = 4
	RPSRetry   byte = 5

	RPSOpen     byte = 8
	RPSReady    byte = 9
	RPSExpired  byte = 10
	RPSHand     byte = 11
	RPSNewRound byte = 12
	RPSClose    byte = 13
)

func RPSScreen(npc int) []byte {
	w := NewWriter(sendRPS)
	w.U8(RPSOpen)
	w.I32(int32(npc))
	return w.Packet()
}

func RPSState(mode byte) []byte {
	w := NewWriter(sendRPS)
	w.U8(mode)
	return w.Packet()
}

func RPSResult(choice, wins byte) []byte {
	w := NewWriter(sendRPS)
	w.U8(RPSHand)
	w.U8(choice)
	w.U8(wins)
	return w.Packet()
}
