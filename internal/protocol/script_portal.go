package protocol

const RecvScriptPortal uint16 = 0xad

// ScriptPortalRequest is emitted by both CField portal senders at 0x87B0D5
// and 0x87B459. Unlike the ordinary 0x40 request it has no target map.
// FieldKey and the coordinate trailer are client observations, not authority.
type ScriptPortalRequest struct {
	FieldKey byte
	Name     string
	X, Y     int16
}

func ReadScriptPortalRequest(r *Reader) (ScriptPortalRequest, error) {
	v := ScriptPortalRequest{FieldKey: r.U8(), Name: r.String()}
	v.X, v.Y = r.I16(), r.I16()
	if r.Err() != nil || r.Remaining() != 0 || len(v.Name) == 0 || len(v.Name) > 64 {
		return v, ErrMalformed
	}
	return v, nil
}
