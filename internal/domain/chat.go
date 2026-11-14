package domain

import "sync/atomic"

var chatUnlimited atomic.Bool

// ChatUnlimited reports whether the operator has lifted the chat flood
// limits: the server's per-player chat budget, and (through the launcher's
// 0x615 ClientUIChatUnlimited bit) the client's own cooldowns.
func ChatUnlimited() bool { return chatUnlimited.Load() }

// SetChatUnlimited switches the chat flood limits.
func SetChatUnlimited(enabled bool) { chatUnlimited.Store(enabled) }

// SetChatUnlimitedForTest switches the setting and returns a restore function.
func SetChatUnlimitedForTest(enabled bool) (restore func()) {
	previous := chatUnlimited.Swap(enabled)
	return func() { chatUnlimited.Store(previous) }
}
