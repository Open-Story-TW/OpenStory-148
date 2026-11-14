package protocol

import (
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

const nativeSnailHit = "0211e803000000fd0bc55a002c8051955cc901061a2968020000000053420f00078001011c028b011e028b01c7010a000000e1839dd701ac81e241028b0100"
const nativeSnailMiss = "0101e803000000fd0bc55a002c0051955cc90106673b58020000000044019b0000"

func TestNativeThreeSnailsMissAndHitDecodePerLine(t *testing.T) {
	for _, raw := range []string{nativeSnailMiss, nativeSnailHit} {
		body := v148Body(RecvMagicAttack, raw)
		a, err := ReadBeginnerMagicAttack(NewReader(body))
		if err != nil || a.SkillID != 1000 || a.TByte&15 != 1 || a.Speed != 6 || hex.EncodeToString(a.Wire) != hex.EncodeToString(body) {
			t.Fatalf("native1000 decode: %+v %v", a, err)
		}
		if len(body) == 67 { // v148: one target, four bytes longer than v145
			if len(a.Targets) != 1 || a.Targets[0].ObjectID != 1000019 || len(a.Targets[0].Lines) != 1 || a.Targets[0].Lines[0].Raw != 10 || a.Tick != 40380698 || a.X != 577 || a.Y != 395 || a.Display != 0x802c {
				t.Fatalf("native hit fields: %+v", a)
			}
		} else if len(a.Targets) != 0 || a.X != 324 || a.Y != 155 {
			t.Fatal("native miss shape")
		}
		for n := 0; n < len(body); n++ {
			if _, err = ReadBeginnerMagicAttack(NewReader(body[:n])); err == nil {
				t.Fatalf("accepted truncated%d/%d", n, len(body))
			}
		}
		if _, err = ReadBeginnerMagicAttack(NewReader(append(body, 0))); err == nil {
			t.Fatal("accepted extra trailer")
		}
	}
	body := v148Body(RecvMagicAttack, nativeSnailHit)
	for _, count := range []byte{0x10, 0x12, 0x21} {
		bad := append([]byte(nil), body...)
		bad[1] = count
		if _, err := ReadBeginnerMagicAttack(NewReader(bad)); err == nil {
			t.Fatal("accepted unreviewed line/target shape")
		}
	}
}

func TestNativeForeignMagicHasExactActorHeaderAndAppliedLine(t *testing.T) {
	body := v148Body(RecvMagicAttack, nativeSnailHit)
	a, err := ReadBeginnerMagicAttack(NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	a.Targets[0].Lines[0].Damage = 5 // Server clips raw10 against remainingHP5.
	packet := MagicAttackPacket(domain.Character{ID: 102, Level: 3}, 1, a)
	if len(packet) != 31 {
		t.Fatalf("native foreign1000 length%d", len(packet))
	}
	r := NewReader(packet)
	if r.U16() != 0x136 || r.I32() != 102 || r.U8() != 0x11 || r.U8() != 3 || r.U8() != 1 || r.I32() != 1000 || r.U8() != 0 || r.U16() != 0x802c || r.U8() != 6 || r.U8() != 0 || r.I32() != -1 || r.I32() != 1000019 || r.U8() != 7 || r.U32() != 5 || r.Remaining() != 0 {
		t.Fatalf("foreign1000 native fields shifted: %x", packet)
	}
}
