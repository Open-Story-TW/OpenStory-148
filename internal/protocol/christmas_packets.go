package protocol

// 0x60F1AE and 0x5EE6B6 in v148 send this request for a friendly
// monster hit. The body is attacker object, character, defender object.
func FriendlyDamage(objectID int32, damage, hp, maxHP int) []byte {
	w := NewWriter(SendDamageMonster)
	w.I32(objectID)
	w.U8(1)
	w.I32(int32(damage))
	w.I32(int32(hp))
	w.I32(int32(maxHP))
	return w.Packet()
}
