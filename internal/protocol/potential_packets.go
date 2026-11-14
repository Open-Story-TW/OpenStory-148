package protocol

const (
	RecvUsePotentialScroll uint16 = 0x98
	RecvUseMagnify         uint16 = 0x9c
	SendMagnifyEffect      uint16 = 0x108
)

// These layouts follow original v145 senders 0x916AA8 / 0x916B27.
// Slot eligibility, trait127, inventory identity and economics are server rules,
// not claims made by the structural decoder. In particular signed worn slots
// must not be truncated to Java's legacy byte representation.
type MagnifyInput struct {
	ClientTick             uint32
	SourceSlot, TargetSlot int
}

type PotentialScrollInput struct {
	ClientTick             uint32
	SourceSlot, TargetSlot int
	LegendarySpirit        bool
}

func ReadMagnifyInput(r *Reader) (MagnifyInput, error) {
	in := MagnifyInput{ClientTick: r.U32(), SourceSlot: int(r.I16()), TargetSlot: int(r.I16())}
	if r.Err() != nil || r.Remaining() != 0 {
		return in, ErrMalformed
	}
	return in, nil
}

func ReadPotentialScrollInput(r *Reader) (PotentialScrollInput, error) {
	in := PotentialScrollInput{ClientTick: r.U32(), SourceSlot: int(r.I16()), TargetSlot: int(r.I16())}
	legendary := r.U8()
	in.LegendarySpirit = legendary != 0
	if r.Err() != nil || r.Remaining() != 0 || legendary > 1 {
		return in, ErrMalformed
	}
	return in, nil
}

// MagnifySuccess is only a successful local UI notification (CID+I16 slot).
// It does not update equipment, deduct a tool/fee or prove a remote effect.
// The caller must publish the actual committed inventory changes separately.
func MagnifySuccess(characterID int64, targetSlot int) []byte {
	if characterID < 1 || characterID > 0x7fffffff || targetSlot == 0 || targetSlot < -32768 || targetSlot > 32767 {
		return nil
	}
	w := NewWriter(SendMagnifyEffect)
	w.I32(int32(characterID))
	w.I16(int16(targetSlot))
	return w.Packet()
}
