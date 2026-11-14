package domain

import "sync/atomic"

// Guild size limits. A guild's capacity is the stored number its master
// raised at the guild office, at least the operator's base capacity and at
// most the operator's ceiling, so both settings apply to every guild at once.
const (
	// NativeGuildCapacity is the ceiling without the large guild contract:
	// the native guild record counts members in one byte, and Legacy stops
	// GP expansion at 200.
	NativeGuildCapacity = 200
	// DefaultGuildCapacityLimit is the guild_max_capacity default.
	DefaultGuildCapacityLimit = 512
	// MaxGuildCapacityLimit keeps every guild packet inside the 64 KiB
	// packet limit: an alliance is created with two full guild records
	// (43 bytes per member) in one packet.
	MaxGuildCapacityLimit = 700
	// GuildMesosCapacity is how far mesos expansion goes; GP goes on to the
	// ceiling.
	GuildMesosCapacity = 100
	// GuildStartCapacity is a new guild's stored capacity.
	GuildStartCapacity = 10
	// GuildCapacityStep is one expansion at the guild office.
	GuildCapacityStep = 5
)

var (
	largeGuild         atomic.Bool
	guildCapacityLimit atomic.Int64
	guildBaseCapacity  atomic.Int64
)

func init() {
	largeGuild.Store(true)
	guildCapacityLimit.Store(DefaultGuildCapacityLimit)
	guildBaseCapacity.Store(GuildStartCapacity)
}

// LargeGuildEnabled reports whether guild member counts are 16-bit on the
// wire (the launcher's LargeGuild contract).
func LargeGuildEnabled() bool { return largeGuild.Load() }

// SetLargeGuild selects 16-bit guild member counts on the wire.
func SetLargeGuild(enabled bool) { largeGuild.Store(enabled) }

// SetLargeGuildForTest switches the contract and returns a restore function.
func SetLargeGuildForTest(enabled bool) (restore func()) {
	previous := largeGuild.Swap(enabled)
	return func() { largeGuild.Store(previous) }
}

// SetGuildCapacity sets the operator's ceiling and base capacity.
func SetGuildCapacity(limit, base int) {
	guildCapacityLimit.Store(int64(limit))
	guildBaseCapacity.Store(int64(base))
}

// SetGuildCapacityForTest changes both and returns a restore function.
func SetGuildCapacityForTest(limit, base int) (restore func()) {
	l, b := guildCapacityLimit.Swap(int64(limit)), guildBaseCapacity.Swap(int64(base))
	return func() { guildCapacityLimit.Store(l); guildBaseCapacity.Store(b) }
}

// GuildCapacityLimit is the most members any guild may have.
func GuildCapacityLimit() int {
	limit := int(guildCapacityLimit.Load())
	top := MaxGuildCapacityLimit
	if !LargeGuildEnabled() {
		top = NativeGuildCapacity
	}
	return max(1, min(limit, top))
}

// GuildMesosCapacityLimit is how far mesos expansion may raise a guild.
func GuildMesosCapacityLimit() int { return min(GuildMesosCapacity, GuildCapacityLimit()) }

// EffectiveGuildCapacity is the capacity of a guild whose stored value is
// stored: raised to the base capacity and cut to the ceiling.
func EffectiveGuildCapacity(stored int) int {
	return min(max(stored, int(guildBaseCapacity.Load())), GuildCapacityLimit())
}

// GuildBaseCapacity is the capacity every guild has without expansion.
func GuildBaseCapacity() int { return int(guildBaseCapacity.Load()) }
