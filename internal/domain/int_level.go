package domain

import (
	"math"
	"os"
	"strconv"
	"sync/atomic"
)

// Stock limits, in force while MAPLEGO_INT_LEVEL=0.
const (
	stockMaxCharacterLevel = 200
	stockMaxWireLevel      = 255
	defaultIntLevelCap     = 999
)

// intLevelCap is 0 while the switch is off. It is on by default (Raymond,
// 2026-11-09) and pairs with the client's [Features] IntLevel=1;
// MAPLEGO_INT_LEVEL=0 turns it off. MAPLEGO_MAX_LEVEL optionally replaces the
// default cap; a value that is not an int32 above 255 keeps the default.
var intLevelCap atomic.Int64

func init() {
	intLevelCap.Store(int64(parseIntLevelCap(os.Getenv("MAPLEGO_INT_LEVEL"), os.Getenv("MAPLEGO_MAX_LEVEL"))))
}

func parseIntLevelCap(enabled, maximum string) int {
	if enabled == "0" {
		return 0
	}
	value, err := strconv.Atoi(maximum)
	if err != nil || value <= stockMaxWireLevel || value > math.MaxInt32 {
		return defaultIntLevelCap
	}
	return value
}

// SetIntLevelCapForTest switches the limit (0 is off) and returns a restore function.
func SetIntLevelCapForTest(limit int) (restore func()) {
	previous := intLevelCap.Swap(int64(limit))
	return func() { intLevelCap.Store(previous) }
}

// IntLevelEnabled reports whether character levels may exceed the stock cap.
func IntLevelEnabled() bool { return intLevelCap.Load() != 0 }

// MaxCharacterLevel is the highest level a character may reach.
func MaxCharacterLevel() int {
	if limit := intLevelCap.Load(); limit != 0 {
		return int(limit)
	}
	return stockMaxCharacterLevel
}

// MaxWireLevel is the highest level a packet writer accepts.
func MaxWireLevel() int {
	if limit := intLevelCap.Load(); limit != 0 {
		return int(limit)
	}
	return stockMaxWireLevel
}

// WireLevelByte is a level for the client's one-byte foreign-level fields. A
// level above the byte's range saturates, which is what the client's own field
// holds; a wrapped value would read as a low-level character.
func WireLevelByte(level int) byte {
	return byte(min(level, stockMaxWireLevel))
}
