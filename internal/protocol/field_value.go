package protocol

// These TWMS v148 CWvsContext handlers read two Maple strings. The native
// field implementations use the variable name to dispatch HUD updates.
func SessionValue(key, value string) []byte { return fieldValue(0x70, key, value) }
func PartyValue(key, value string) []byte   { return fieldValue(0x71, key, value) }
func MapValue(key, value string) []byte     { return fieldValue(0x72, key, value) }

func fieldValue(opcode uint16, key, value string) []byte {
	w := NewWriter(opcode)
	w.String(key)
	w.String(value)
	return w.Packet()
}
