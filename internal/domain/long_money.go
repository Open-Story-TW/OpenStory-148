package domain

import (
	"math"
	"os"
	"sync/atomic"
)

// longMoney selects the 64-bit balance contract for mesos, storage mesos and
// the cash wallet. It must match the launcher, because it changes packet
// layouts; the launcher takes it from the client config packet.
var longMoney atomic.Bool

func init() { longMoney.Store(os.Getenv("MAPLEGO_LONG_MONEY") != "0") }

// LongMoneyEnabled reports whether balances are 64-bit on the wire.
func LongMoneyEnabled() bool { return longMoney.Load() }

// SetLongMoney selects 64-bit balances on the wire.
func SetLongMoney(enabled bool) { longMoney.Store(enabled) }

// SetLongMoneyForTest switches the contract and returns a restore function.
func SetLongMoneyForTest(enabled bool) (restore func()) {
	previous := longMoney.Swap(enabled)
	return func() { longMoney.Store(previous) }
}

// MaxMoney is the highest balance a character, a storage or a wallet may
// hold, and one trade offer or storage transfer. A price and a ground drop
// stay 32-bit.
func MaxMoney() int64 {
	if LongMoneyEnabled() {
		return math.MaxInt64
	}
	return math.MaxInt32
}
