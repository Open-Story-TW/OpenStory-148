package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/domain"
)

func TestLocalResetCarriesEmptyIndieLists(t *testing.T) {
	local, err := TemporaryStatReset(0, []buffstat.Stat{"ANGEL_ATK", "ANGEL_MATK"})
	if err != nil {
		t.Fatal(err)
	}
	// opcode + mask + two empty indie lists + movement/riding bytes.
	if len(local) != 2+32+4+4+2 {
		t.Fatalf("local indie reset width %d", len(local))
	}
	for _, b := range local[34:42] {
		if b != 0 {
			t.Fatal("indie list count not zero")
		}
	}
	remote, err := TemporaryStatReset(7, []buffstat.Stat{"ANGEL_ATK"})
	if err != nil || len(remote) != 2+4+32+2 {
		t.Fatalf("remote reset reads no indie data: %d %v", len(remote), err)
	}
	plain, err := TemporaryStatReset(0, []buffstat.Stat{"WATK"})
	if err != nil || len(plain) != 2+32+2 {
		t.Fatalf("non-indie reset changed: %d %v", len(plain), err)
	}
}

func TestMechResetAlsoResetsBit137(t *testing.T) {
	p, err := TemporaryStatReset(0, []buffstat.Stat{"MECH_CHANGE"})
	if err != nil {
		t.Fatal(err)
	}
	word := func(bit int) uint32 {
		o := 2 + (7-bit/32)*4
		return uint32(p[o]) | uint32(p[o+1])<<8 | uint32(p[o+2])<<16 | uint32(p[o+3])<<24
	}
	if word(107)&(1<<(107%32)) == 0 || word(137)&(1<<(137%32)) == 0 {
		t.Fatal("mech reset mask lacks MECH_CHANGE or bit 137")
	}
}

func TestQADebuffRowsEncodeLocallyAndRemotely(t *testing.T) {
	now := time.Now()
	for _, stat := range []string{"POISON", "SEAL", "STUN", "CURSE", "SLOW", "DARKNESS", "WEAKEN", "SEDUCE", "FREEZE"} {
		rows := []domain.SkillBuff{{ID: 125 | 1<<16, Level: 1, Stat: stat, Value: 1, ExpiresAt: now.Add(time.Minute)}}
		if SkillBuffs(rows, now) == nil || stat != "POISON" && ForeignSkillBuffs(7, rows) == nil {
			t.Fatalf("%s debuff has no packet", stat)
		}
	}
}

func TestTwoStateTermRoundsUpSubSecondRemainders(t *testing.T) {
	p, err := TemporaryStatSet(4321000, 950*time.Millisecond, []BuffStat{{Stat: "DASH_SPEED", Value: 155}})
	if err != nil {
		t.Fatal(err)
	}
	// opcode, mask, 3 bytes, two-state (value, reason, U8, I32, U16 term), delay, move byte.
	term := int(p[2+32+3+4+4+1+4]) | int(p[2+32+3+4+4+1+4+1])<<8
	if term != 1 {
		t.Fatalf("term %d", term)
	}
}
