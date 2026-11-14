package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestProfessionSkillRecordsRideTheSkillSection(t *testing.T) {
	c := domain.Character{
		Skills:      []domain.SkillState{{ID: 1000, Level: 3}},
		Professions: []domain.ProfessionState{{ID: 92000000, Level: 2, EXP: 300}},
		Recipes:     []int{92040011},
	}
	w := NewWriter(0)
	AddSkillSections(w, c, time.Unix(0, 0))
	r := NewReader(w.Packet())
	r.U16()
	if r.U8() != 0 || r.U16() != 3 {
		t.Fatal("skill record count")
	}
	want := [][2]int32{{1000, 3}, {92000000, 2<<24 | 300}, {92040011, 1}}
	for _, v := range want {
		if id, level := r.I32(), r.I32(); id != v[0] || level != v[1] {
			t.Fatalf("record %d = %d, want %d", id, level, v[1])
		}
	}
	// No profession: the section is exactly the job skills.
	plain := NewWriter(0)
	AddSkillSections(plain, domain.Character{Skills: c.Skills}, time.Unix(0, 0))
	if r = NewReader(plain.Packet()); r.U16() != 0 || r.U8() != 0 || r.U16() != 1 {
		t.Fatal("plain skill section changed")
	}
}

func TestProfessionChangePackets(t *testing.T) {
	before := domain.Character{
		Professions: []domain.ProfessionState{{ID: 92000000, Level: 1, EXP: 240}, {ID: 92040000, Level: 1}},
		Recipes:     []int{92040011},
		Traits:      domain.Traits{Sense: 10},
		Fatigue:     7,
	}
	after := before
	after.Professions = []domain.ProfessionState{{ID: 92000000, Level: 2, EXP: 10}}
	after.Recipes = nil
	after.Traits = domain.Traits{Sense: 15, Craft: 40}
	after.Fatigue = 8
	packets := ProfessionChanges(before, after)
	if len(packets) != 3 {
		t.Fatalf("packets = %d", len(packets))
	}
	// Skill records: herbalism at its new value, alchemy removed, the recipe forgotten.
	r := NewReader(packets[0])
	if r.U16() != 0x31 || r.U8() != 0 || r.U8() != 0 || r.U16() != 3 {
		t.Fatal("skill record header")
	}
	for _, v := range [][2]int32{{92000000, 2<<24 | 10}, {92040000, 0}, {92040011, -1}} {
		if id, level, master, expiry := r.I32(), r.I32(), r.I32(), r.I64(); id != v[0] || level != v[1] || master != 0 || expiry != maxTime {
			t.Fatalf("record %d level %d", id, level)
		}
	}
	if r.U8() != 4 || r.Remaining() != 0 {
		t.Fatal("skill record tail")
	}
	// Stats: fatigue byte, then craft and sense in mask order.
	if want := "2c00" + "00" + "0000880100000000" + "08" + "28000000" + "0f000000" + "0000"; hex.EncodeToString(packets[1]) != want {
		t.Fatalf("stats = %x", packets[1])
	}
	// Message 16: +40 craft, +5 sense.
	if want := "3600" + "10" + "0000800100000000" + "28000000" + "05000000"; hex.EncodeToString(packets[2]) != want {
		t.Fatalf("trait gain = %x", packets[2])
	}
	if ProfessionChanges(after, after) != nil || TraitStats(after, after) != nil || TraitGain(after.Traits, before.Traits) != nil {
		t.Fatal("unchanged state produced packets")
	}
}

func TestProfessionWireLayouts(t *testing.T) {
	for name, c := range map[string]struct {
		got  []byte
		want string
	}{
		"open ui":        {OpenCraftingWindow(), "6201" + "2a000000"},
		"craft started":  {CraftStarted(7, 418, 4000), "1301" + "07000000" + "a2010000" + "a00f0000"},
		"craft success":  {CraftComplete(7, 92000002, CraftGood, 4023000, 1, 20), "1401" + "07000000" + "02cf7b05" + "15000000" + "d8623d00" + "01000000" + "14000000"},
		"craft failure":  {CraftComplete(7, 92000002, CraftFailed, 4023000, 1, 0), "1401" + "07000000" + "02cf7b05" + "18000000" + "00000000"},
		"harvested":      {Harvested(7, true), "1501" + "07000000" + "01"},
		"harvesting on":  {ShowHarvesting(7, 1502000), "4b01" + "07000000" + "01" + "30eb1600" + "00000000"},
		"harvesting off": {ShowHarvesting(7, 0), "4b01" + "07000000" + "00"},
		"harvest start":  {HarvestMessage(9, HarvestStart), "7801" + "09000000" + "0b000000"},
		"reactor enter":  {ReactorEnter(9, 100000, 0, 830, 208, 0), "ef01" + "09000000" + "a0860100" + "00" + "3e03" + "d000" + "00" + "0000"},
		"reactor state":  {ReactorState(9, 2, 830, 208), "ed01" + "09000000" + "02" + "3e03" + "d000" + "0000" + "00" + "04"},
		"reactor leave":  {ReactorLeave(9, 4, 830, 208), "f101" + "09000000" + "04" + "3e03" + "d000"},
	} {
		if hex.EncodeToString(c.got) != c.want {
			t.Errorf("%s = %x, want %s", name, c.got, c.want)
		}
	}
	body := NewWriter(RecvProfessionInfo)
	body.String("92020000_premium")
	body.I32(6)
	body.I32(2)
	r := NewReader(body.Packet())
	r.U16()
	q, err := ReadProfessionInfo(r)
	if err != nil || q.Profession != 92020000 || q.Required != 6 || q.Current != 2 {
		t.Fatalf("info request: %+v %v", q, err)
	}
	reply := ProfessionInfo(q, 20)
	if !bytes.HasPrefix(reply, []byte{0x8e, 0x00, 16, 0}) || !bytes.HasSuffix(reply, []byte{6, 0, 0, 0, 2, 0, 0, 0, 1, 20, 0, 0, 0}) {
		t.Fatalf("info reply = %x", reply)
	}
	if _, err = ReadProfessionInfo(NewReader([]byte{3, 0, 'a', 'b', 'c', 0, 0, 0, 0, 0, 0, 0, 0})); err == nil {
		t.Fatal("malformed info request accepted")
	}
	if _, _, err = ReadUseRecipe(NewReader([]byte{1, 0, 0, 0, 3, 0, 0x40, 0x4f, 0x26, 0})); err != nil {
		t.Fatalf("use recipe: %v", err)
	}
	if _, _, err = ReadUseRecipe(NewReader([]byte{1, 0, 0, 0, 3, 0, 0x80, 0x84, 0x1e, 0})); err == nil {
		t.Fatal("a potion was read as a recipe item")
	}
}
