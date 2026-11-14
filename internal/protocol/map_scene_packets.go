package protocol

import "strings"

// Native CUserLocal 0x866A9F reads just the flag. Java appends four unused
// bytes; packet framing makes those harmless, but they are not required.
func SceneKeyLock(locked bool) []byte {
	w := NewWriter(0x165)
	w.Bool(locked)
	return w.Packet()
}

// Native 0x86E77D: enabled; if enabled, lock-user-input and direction mode.
func SceneUILock(locked bool) []byte {
	w := NewWriter(0x166)
	w.Bool(locked)
	if locked {
		w.U8(1)
		w.U8(0)
	}
	return w.Packet()
}

func SceneEffect(path string) []byte {
	if len(path) == 0 || len(path) > 160 || !strings.HasPrefix(path, "Effect/Direction") || strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00") {
		return nil
	}
	w := NewWriter(0x154)
	w.U8(22)
	w.String(path)
	return w.Packet()
}
