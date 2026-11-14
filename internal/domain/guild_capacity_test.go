package domain

import "testing"

func TestEffectiveGuildCapacity(t *testing.T) {
	defer SetLargeGuildForTest(true)()
	defer SetGuildCapacityForTest(DefaultGuildCapacityLimit, GuildStartCapacity)()
	for _, c := range []struct{ stored, want int }{{1, 10}, {10, 10}, {150, 150}, {512, 512}, {600, 512}} {
		if got := EffectiveGuildCapacity(c.stored); got != c.want {
			t.Fatalf("stored %d: %d, want %d", c.stored, got, c.want)
		}
	}
	if GuildMesosCapacityLimit() != 100 || GuildCapacityLimit() != 512 {
		t.Fatal("expansion limits")
	}
	SetGuildCapacity(300, 300)
	if EffectiveGuildCapacity(10) != 300 || EffectiveGuildCapacity(400) != 300 {
		t.Fatal("base equal to the ceiling opens every guild fully")
	}
	SetGuildCapacity(5000, 10)
	if GuildCapacityLimit() != MaxGuildCapacityLimit {
		t.Fatal("ceiling above the packet limit")
	}
	SetLargeGuild(false)
	if GuildCapacityLimit() != NativeGuildCapacity || EffectiveGuildCapacity(250) != NativeGuildCapacity {
		t.Fatal("native ceiling")
	}
}
