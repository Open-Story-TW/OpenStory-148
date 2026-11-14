package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// A stacked indie stat sends every active source in one list, the packet's
// own source first; a single source keeps the one-entry layout.
func TestSkillBuffsAmongListsEverySource(t *testing.T) {
	now := time.Now()
	pill := domain.SkillBuff{ID: -2022002, Level: 1, Stat: "ANGEL_ATK", Value: 10, ExpiresAt: now.Add(3 * time.Minute)}
	rage := domain.SkillBuff{ID: 1101006, Level: 1, Stat: "ANGEL_ATK", Value: 20, ExpiresAt: now.Add(time.Minute)}
	gone := domain.SkillBuff{ID: 1001003, Level: 1, Stat: "ANGEL_ATK", Value: 99, ExpiresAt: now.Add(-time.Second)}
	if string(SkillBuffsAmong([]domain.SkillBuff{pill}, []domain.SkillBuff{pill}, now)) != string(SkillBuffs([]domain.SkillBuff{pill}, now)) {
		t.Fatal("single source changed bytes")
	}
	r := NewReader(SkillBuffsAmong([]domain.SkillBuff{pill}, []domain.SkillBuff{rage, pill, gone}, now))
	r.U16()
	r.Skip(32 + 3)
	if n := r.I32(); n != 2 {
		t.Fatalf("entries %d", n)
	}
	for _, want := range []domain.SkillBuff{pill, rage} {
		src, v := r.I32(), r.I32()
		r.I32()
		ms := r.I32()
		if int(src) != want.ID || int(v) != want.Value || ms < int32(want.ExpiresAt.Sub(now).Milliseconds())-1000 {
			t.Fatalf("entry %d %d %d", src, v, ms)
		}
	}
	r.U16()
	r.U8() // v148 byte after the delay
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("layout %v %d", r.Err(), r.Remaining())
	}
}

func TestReadItemCancel(t *testing.T) {
	w := NewWriter(RecvItemCancel)
	w.I32(-2022002)
	r := NewReader(w.Packet())
	r.U16()
	if id, err := ReadItemCancel(r); err != nil || id != -2022002 {
		t.Fatalf("item cancel %d %v", id, err)
	}
	for _, bad := range []int32{1001003, -1999999, -3000000, 0} {
		w := NewWriter(RecvItemCancel)
		w.I32(bad)
		r := NewReader(w.Packet())
		r.U16()
		if _, err := ReadItemCancel(r); err == nil {
			t.Fatalf("accepted %d", bad)
		}
	}
}
