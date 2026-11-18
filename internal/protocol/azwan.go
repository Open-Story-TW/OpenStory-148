package protocol

// HonorProgress is CWvsContext::OnCharacterHonorLevel in the v148 client.
func HonorProgress(level, experience int, leveledUp bool) []byte {
	w := NewWriter(0xA1)
	w.I32(int32(level))
	w.I32(int32(experience))
	w.Bool(leveledUp)
	return w.Packet()
}
