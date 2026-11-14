package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestReadCharInfoRequest(t *testing.T) {
	w := NewWriter(RecvCharInfoRequest)
	w.I32(123456) // update time
	w.I32(4903)
	w.U8(1)
	w.U8(0)
	r := NewReader(w.Packet())
	if r.U16() != RecvCharInfoRequest {
		t.Fatal("opcode")
	}
	id, err := ReadCharInfoRequest(r)
	if err != nil || id != 4903 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	short := NewReader([]byte{1, 2, 3})
	if _, err = ReadCharInfoRequest(short); err == nil {
		t.Fatal("short request accepted")
	}
}

// decodeCharInfo reads the reply the way v148 0x9E1F4B and its helpers do.
func decodeCharInfo(t *testing.T, packet []byte, level32 bool) (level int, guild, alliance string, pets []int32, medal int32) {
	t.Helper()
	r := NewReader(packet)
	if r.U16() != SendCharInfo {
		t.Fatal("opcode")
	}
	r.I32()
	if level32 {
		level = int(r.I32())
	} else {
		level = int(r.U8())
	}
	r.U16()
	r.U16()
	r.U8()
	r.I32()
	r.U8()
	for n := r.U8(); n > 0; n-- {
		r.U16()
	}
	guild, alliance = r.String(), r.String()
	r.U8()
	r.U8()
	r.String()
	for range 6 {
		r.U8()
	}
	for r.U8() != 0 {
		pets = append(pets, r.I32())
		r.I32()
		r.String()
		r.U8()
		r.U16()
		r.U8()
		r.U16()
		r.I32()
	}
	if r.U8() != 0 {
		r.I32()
		r.I32()
		r.I32()
	}
	for n := r.U8(); n > 0; n-- {
		r.I32()
	}
	medal = r.I32()
	for n := r.U16(); n > 0; n-- {
		r.U16()
		r.I64()
	}
	for range 6 {
		r.U8()
	}
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("decode err=%v remaining=%d", r.Err(), r.Remaining())
	}
	return
}

func TestCharInfoMatchesV148Decoder(t *testing.T) {
	c := domain.Character{
		ID: 4903, Level: 300, Job: 132, Fame: 12,
		Guild:     domain.GuildTag{ID: 7, Name: "楓之谷"},
		Equipment: []domain.Equipment{{ItemID: 1142000, Slot: -46}},
		Pets: []domain.Pet{
			{TemplateID: 5000000, Name: "小狗", Summon: 2, Level: 5},
			{TemplateID: 5000001, Name: "休息", Summon: 0},
		},
	}
	for _, level32 := range []bool{true, false} {
		restore := domain.SetLevel32ForTest(level32)
		level32 = domain.Level32Wire() // the int-level feature keeps it on
		packet := CharInfo(c, "聯盟", false)
		restore()
		if packet == nil {
			t.Fatal("nil packet")
		}
		level, guild, alliance, pets, medal := decodeCharInfo(t, packet, level32)
		want := 300
		if !level32 {
			want = 255
		}
		if level != want || guild != "楓之谷" || alliance != "聯盟" || len(pets) != 1 || pets[0] != 1 || medal != 1142000 {
			t.Fatalf("level32=%v level=%d guild=%q alliance=%q pets=%v medal=%d", level32, level, guild, alliance, pets, medal)
		}
	}
	restore := domain.SetLevel32ForTest(true)
	defer restore()
	_, guild, alliance, pets, _ := decodeCharInfo(t, CharInfo(domain.Character{ID: 1, Level: 10}, "ignored", true), true)
	if guild != "-" || alliance != "" || len(pets) != 0 {
		t.Fatalf("no guild: guild=%q alliance=%q pets=%v", guild, alliance, pets)
	}
}
