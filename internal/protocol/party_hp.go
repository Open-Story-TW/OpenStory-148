package protocol

import "maple-go-server/internal/domain"

// Native UserPool0x8AC895 consumes cid, then case304 at0x8ACA03 calls
// CUserRemote::OnReceiveHP0x8B3713 for current/max I32. Its setter divides
// by maxHP at0x8B331E/0x8B3344, so a zero maximum must never reach the client.
func PartyMemberHP(id int64, hp, maxHP int) []byte {
	if !validPartyID(id) || hp < 0 || maxHP < 1 || hp > maxHP || maxHP > domain.VitalCeiling {
		return nil
	}
	w := NewWriter(0x147)
	w.I32(int32(id))
	w.I32(int32(hp))
	w.I32(int32(maxHP))
	return w.Packet()
}
