package protocol

import "maple-go-server/internal/domain"

const RecvUseUpgradeScroll uint16 = 0x94
const SendScrollEffect uint16 = 0x106

func ReadScrollRequest(r *Reader) (domain.ScrollRequest, error) {
	v := domain.ScrollRequest{ClientTick: r.U32(), SourceSlot: int(r.I16()), TargetSlot: int(r.I16()), WhiteFlags: int(r.I16())}
	legendary := r.U8()
	v.LegendarySpirit = legendary != 0
	if r.Err() != nil || r.Remaining() != 0 || legendary > 1 {
		return v, ErrMalformed
	}
	return v, nil
}

func ScrollEffect(characterID int64, result domain.ScrollResult) []byte {
	w := NewWriter(SendScrollEffect)
	w.I32(int32(characterID))
	w.U8(byte(result.Status))
	w.Bool(result.LegendarySpirit)
	w.I32(int32(result.ScrollID))
	w.I32(int32(result.EquipmentTemplateID))
	// Native CUser::ShowItemUpgradeEffect0x83DFA3 reads two byte flags.
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
