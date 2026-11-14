package protocol

// RevivalRequest describes the native death dialog's field-transfer choice.
// Only the zero-cost ordinary return is currently supported by the host.
type RevivalRequest struct {
	WheelType uint32
}
