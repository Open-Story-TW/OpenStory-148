package protocol

// The v148 mob-on-mob senders at 0x5EE6B6 and 0x60F1AE write the
// attacker OID, local character ID and target OID. The separate 0x165
// request at 0x5FE5D5 describes a mob hitting a field object.
const RecvMobHitEscort uint16 = 0x167

type MobHitEscort struct {
	Attacker, CharacterID, Escort int32
}

func ReadMobHitEscort(r *Reader) (MobHitEscort, error) {
	h := MobHitEscort{Attacker: r.I32(), CharacterID: r.I32(), Escort: r.I32()}
	if r.Err() != nil || r.Remaining() != 0 || h.Attacker <= 0 || h.CharacterID <= 0 || h.Escort <= 0 || h.Attacker == h.Escort {
		return MobHitEscort{}, ErrMalformed
	}
	return h, nil
}

func DamageEscort(objectID int32, damage, hp, maxHP int) []byte {
	// 0x1B5 enters 0x609ABD after CMobPool has consumed the object ID.
	w := NewWriter(0x1b5)
	w.I32(objectID)
	w.U8(1)
	w.I32(int32(damage))
	w.I32(int32(hp))
	w.I32(int32(maxHP))
	return w.Packet()
}
