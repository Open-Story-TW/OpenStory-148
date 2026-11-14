package protocol

import (
	"strings"
	"unicode"
)

const RecvGeneralChat uint16 = 0x5c
const SendGeneralChat uint16 = 0x102

type GeneralChatRequest struct {
	ClientTick uint32
	Text       string
	Show       bool
}

// Native sub_4F935F (0x4F93C9..0x4F940C) writes tick, Big5 string, then show.
// The 256-byte message bound is a server resource limit, not a native UI claim.
func ReadGeneralChat(r *Reader) (GeneralChatRequest, error) {
	var req GeneralChatRequest
	if r.Remaining() > 4+2+256+1 {
		return req, ErrMalformed
	}
	req.ClientTick = r.U32()
	req.Text = r.String()
	show := r.U8()
	req.Show = show != 0
	if r.Err() != nil || r.Remaining() != 0 || show > 1 {
		return req, ErrMalformed
	}
	if strings.ContainsFunc(req.Text, func(r rune) bool { return unicode.IsControl(r) || r == unicode.ReplacementChar }) {
		return req, ErrMalformed
	}
	return req, nil
}

// Native CUserPool::OnUserCommonPacket (0x8AE121) consumes character ID and
// 0x848840 consumes white-background byte, string, and show byte.
func GeneralChat(characterID int64, text string, show bool) []byte {
	if characterID < 1 || characterID > 2147483647 || text == "" {
		return nil
	}
	w := NewWriter(SendGeneralChat)
	w.I32(int32(characterID))
	w.Bool(false) // Ordinary player speech cannot request the staff background.
	w.String(text)
	w.Bool(show)
	return w.Packet()
}
