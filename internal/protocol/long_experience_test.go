package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestExperiencePacketsKeepInt32WithSwitchOff(t *testing.T) {
	defer domain.SetLongExperienceForTest(false)()

	if got, want := len(UpdateEXP(domain.Character{EXP: 7})), 2+1+8+4+1+1; got != want {
		t.Fatalf("UpdateEXP length %d, want %d", got, want)
	}
	if ShowExperienceGain(int64(1)<<31, true) != nil {
		t.Fatal("a gain above int32 must not be sent while the switch is off")
	}
	if domain.MaxExperience() != 2147483647 {
		t.Fatalf("MaxExperience %d", domain.MaxExperience())
	}
}

func TestExperiencePacketsCarryInt64WithSwitchOn(t *testing.T) {
	defer domain.SetLongExperienceForTest(true)()
	const value = int64(5_000_000_000)

	r := NewReader(UpdateEXP(domain.Character{EXP: value})[2:])
	if r.U8() != 1 || r.I64() != 0x10000 || r.I64() != value || r.U8() != 0 || r.U8() != 0 {
		t.Fatal("UpdateEXP does not carry a 64-bit EXP")
	}

	r = NewReader(ShowExperienceGain(value, true)[2:])
	if r.U8() != 3 || r.U8() != 1 || r.I64() != value || r.U8() != 0 || r.I32() != 0 {
		t.Fatal("ShowExperienceGain does not carry a 64-bit gain")
	}
}

func TestAddExperienceStopsAtTheActiveCeiling(t *testing.T) {
	restore := domain.SetLongExperienceForTest(false)
	if got := domain.AddExperience(2147483000, 1000); got != 2147483647 {
		t.Fatalf("int32 ceiling: %d", got)
	}
	restore()

	defer domain.SetLongExperienceForTest(true)()
	if got := domain.AddExperience(2147483000, 1000); got != 2147484000 {
		t.Fatalf("no int32 ceiling expected: %d", got)
	}
	if got := domain.AddExperience(domain.MaxExperience()-1, 5); got != domain.MaxExperience() {
		t.Fatalf("int64 ceiling: %d", got)
	}
}
