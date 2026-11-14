package protocol

// NPCShopRequest is the complete native CShopDlg 0x62 input. DisplayedUnitPrice
// is an assertion of the UI row, never authority for the actual debit.
type NPCShopRequest struct {
	Mode               byte
	Index              int
	TemplateID         int
	Quantity           int
	Slot               int
	DisplayedUnitPrice int64
}

func ReadNPCShopRequest(r *Reader) (NPCShopRequest, error) {
	value := NPCShopRequest{Mode: r.U8()}
	switch value.Mode {
	case 0:
		value.Index = int(r.U16())
		value.TemplateID = int(r.I32())
		value.Quantity = int(r.I16())
		value.DisplayedUnitPrice = int64(r.I32())
	case 1:
		value.Slot = int(r.I16())
		value.TemplateID = int(r.I32())
		value.Quantity = int(r.I16())
	case 2:
		value.Slot = int(r.I16())
	case 3:
	default:
		return NPCShopRequest{}, ErrMalformed
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return NPCShopRequest{}, ErrMalformed
	}
	return value, nil
}
