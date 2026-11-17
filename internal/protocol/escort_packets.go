package protocol

import "maple-go-server/internal/content"

// The v148 mob-on-mob senders at 0x5EE6B6 and 0x60F1AE write the
// attacker OID, local character ID and target OID. The separate 0x165
// request at 0x5FE5D5 describes a mob hitting a field object.
const RecvMobHitEscort uint16 = 0x167

const (
	RecvEscortAttack      uint16 = 0x169
	RecvEscortNode        uint16 = 0x16c
	RecvEscortPathRequest uint16 = 0x16d
	SendEscortPath        uint16 = 0x1bf
)

// EscortPath is decoded by TWMS v148 CMob at 0x5FDE96. The path is a
// linear node array. The last DWORD before the flags is the next node index.
func EscortPath(objectID int32, x, y, next int, nodes []content.EscortNode, stopMillis int) []byte {
	w := NewWriter(SendEscortPath)
	w.I32(objectID)
	w.I32(int32(len(nodes)))
	w.I32(int32(x))
	w.I32(int32(y))
	for _, node := range nodes {
		w.I32(int32(node.X))
		w.I32(int32(node.Y))
		w.I32(int32(node.Attribute))
		if node.Attribute == 2 {
			w.I32(int32(node.StopMillis))
		}
	}
	w.I32(int32(next))
	if stopMillis > 0 {
		w.U8(1)
		w.I32(int32(stopMillis))
	} else {
		w.U8(0)
	}
	w.U8(0)
	return w.Packet()
}

// ResumeEscort clears the two stop flags in v148 CMob at 0x5F8223.
func ResumeEscort(objectID int32) []byte {
	w := NewWriter(0x1c0)
	w.I32(objectID)
	return w.Packet()
}

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

// ReadEscortAttack decodes the v148 wolf attack at 0x60782C. After the
// object IDs come attack metadata and a hit position supplied by the client.
func ReadEscortAttack(r *Reader) (MobHitEscort, error) {
	h := MobHitEscort{Attacker: r.I32(), CharacterID: r.I32(), Escort: r.I32()}
	r.Bytes(11)
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
