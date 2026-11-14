package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// Follow the native local/remote consumers, including the caller's reads after
// SecondaryStat decoding. The live1002 failure reached that last local read;
// checking only the common SecondaryStat suffix had missed it.
func consumeReviewedBuff(packet []byte, remote bool) error {
	r := NewReader(packet)
	if remote {
		if r.U16() != 0x145 || r.I32() <= 0 {
			return ErrMalformed
		}
	} else if r.U16() != 0x2d {
		return ErrMalformed
	}
	var mask [8]uint32
	for i := range mask {
		mask[i] = r.U32()
	}
	switch {
	case mask[7] == 0x80:
		if remote {
			r.U8()
		} else {
			r.I16()
			r.I32()
			r.I32()
		}
	case mask[7] == 2, mask[6] == 4:
		if !remote {
			r.I16()
			r.I32()
			r.I32()
		}
	default:
		return ErrMalformed
	}
	r.Skip(3) // unconditional SecondaryStat suffix
	r.U16()   // native OnTemporaryStatSet delay
	if !remote {
		r.U8() // v148 0x9D3128 byte after the delay
	}
	if !remote && mask[7]&0x80 != 0 {
		r.U8() // 0x939817 -> sub_6AD699 movement-mask branch
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return ErrMalformed
	}
	return nil
}

func TestLiveSpeedEOFRegressionWithNativeConsumer(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, b := range []domain.SkillBuff{
		{ID: 1002, Level: 3, Stat: "SPEED", Value: 20, ExpiresAt: now.Add(12 * time.Second)},
		{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: now.Add(110 * time.Second)},
		{ID: 1001, Level: 1, Stat: "RECOVERY", Value: 4, ExpiresAt: now.Add(30 * time.Second)},
	} {
		local := SkillBuff(b, now)
		if err := consumeReviewedBuff(local, false); err != nil {
			t.Fatalf("local%s: %v", b.Stat, err)
		}
		if err := consumeReviewedBuff(ForeignSkillBuff(102, b), true); err != nil {
			t.Fatalf("remote%s: %v", b.Stat, err)
		}
		if b.Stat == "SPEED" && consumeReviewedBuff(local[:len(local)-1], false) == nil {
			t.Fatal("live49-byte speed packet must fail its native final read")
		}
	}
}
