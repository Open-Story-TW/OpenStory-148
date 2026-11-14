package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestFirstJobStatPacketKeepsNativeFieldWidths(t *testing.T) {
	defer domain.SetLongStatsForTest(false)()
	c := domain.Character{Job: 100, STR: 4, DEX: 4, INT: 4, LUK: 4, HP: 430, MaxHP: 430, MP: 104, MaxMP: 104, AP: 57, SP: 1}
	packet := JobAdvancementStats(c)
	if len(packet) != 43 {
		t.Fatalf("native stat packet length: %d", len(packet))
	}
	r := NewReader(packet)
	if r.U16() != SendUpdateStats || r.U8() != 1 || r.I64() != 0xffe0 || r.U16() != 100 {
		t.Fatal("job stat prefix")
	}
	for range 4 {
		if r.I16() != 4 {
			t.Fatal("ability stat width")
		}
	}
	for _, want := range []int32{430, 430, 104, 104} {
		if r.I32() != want {
			t.Fatal("vital stat width")
		}
	}
	if r.I16() != 57 || r.U16() != 1 || r.U8() != 0 || r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("AP/SP or stat trailer")
	}
}

func TestFirstAdventurerJobsCanReenterWorldWithRegularSPPool(t *testing.T) {
	c := domain.Character{ID: 101, Name: "WarriorQA", Level: 10, Job: 100, STR: 4, DEX: 4, INT: 4, LUK: 4, HP: 430, MaxHP: 430, MP: 104, MaxMP: 104, AP: 54, SP: 1, MapID: 102000003}
	for _, job := range []int{100, 200, 300, 400, 500} {
		c.Job = job
		if !validCharacter(c) || len(SetField(c, 1)) == 0 {
			t.Fatalf("supported first job %d rejected during reentry", job)
		}
	}
	c.Job = 610
	if validCharacter(c) || SetField(c, 1) != nil {
		t.Fatal("nonexistent job accepted by full character encoder")
	}
}

func TestExtendedSPJobsWriteBookPairs(t *testing.T) {
	defer domain.SetLongStatsForTest(false)()
	c := domain.Character{Job: 2211, STR: 4, DEX: 4, INT: 4, LUK: 4, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, SP: 3, SPBooks: domain.SPBookTable{1: 4}}
	r := NewReader(JobAdvancementStats(c))
	r.U16()
	r.U8()
	r.I64()
	if r.U16() != 2211 {
		t.Fatal("job")
	}
	for range 4 {
		r.I16()
	}
	for range 4 {
		r.I32()
	}
	r.I16()
	// Book 1 parks 4 SP, the live pool belongs to 2211's book 2.
	if r.U8() != 2 || r.U8() != 2 || r.U8() != 4 || r.U8() != 3 || r.U8() != 3 || r.U8() != 0 || r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("extended SP layout")
	}
	empty := NewWriter(0)
	writeSP(empty, domain.Character{Job: 3000})
	if p := empty.Packet(); len(p) != 3 || p[2] != 0 {
		t.Fatalf("level-1 resistance SP %x", p)
	}
	regular := NewWriter(0)
	writeSP(regular, domain.Character{Job: 2000, SP: 7})
	if p := regular.Packet(); len(p) != 4 || p[2] != 7 || p[3] != 0 {
		t.Fatalf("aran keeps U16 pool %x", p)
	}
}

func TestNewFamilyBeginnersEncodeSubJob(t *testing.T) {
	// The golden bytes are the stock four-byte EXP layout.
	defer domain.SetLongExperienceForTest(false)()
	defer domain.SetLevel32ForTest(true)() // launcher level32 contract
	defer domain.SetLongStatsForTest(false)()
	base := domain.Character{ID: 7, Name: "FamilyQA", Level: 1, STR: 12, DEX: 5, INT: 4, LUK: 4, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000}
	for _, tc := range []struct{ job, sub int }{{1000, 0}, {2000, 0}, {2001, 0}, {3000, 0}, {0, 1}, {0, 2}} {
		c := base
		c.Job, c.SubJob = tc.job, tc.sub
		if !validCharacter(c) || len(SetField(c, 1)) == 0 {
			t.Fatalf("job %d/%d rejected", tc.job, tc.sub)
		}
		w := NewWriter(0)
		addCharStats(w, c, time.Unix(0, 0))
		r := NewReader(w.Packet())
		r.U16()
		r.I32()
		r.Bytes(15)
		r.U8()
		r.U8()
		r.I32()
		r.I32()
		r.I32()
		if r.U16() != uint16(tc.job) {
			t.Fatal("job")
		}
		r.Bytes(4*2 + 4*4 + 2)
		if domain.ExtendedSPJob(tc.job) {
			if r.U8() != 0 {
				t.Fatal("empty SP books")
			}
		} else if r.U16() != 0 {
			t.Fatal("SP pool")
		}
		r.I32()
		r.I32()
		r.I32()
		r.I64()
		if r.I32() != 10000 || r.U8() != 0 || r.U16() != uint16(tc.sub) || r.Err() != nil {
			t.Fatalf("map/portal/sub job for %d/%d", tc.job, tc.sub)
		}
	}
	for _, job := range []int{600, 800, 900, 1001, 2113, 2219, 3002, 3400} {
		c := base
		c.Job = job
		if validCharacter(c) {
			t.Fatalf("job %d does not exist in v145", job)
		}
	}
}

func TestDemonMarkFollowsSubJobAndAvatarLook(t *testing.T) {
	c := domain.Character{ID: 9, Name: "DemonQA", Level: 10, Job: 3100, STR: 4, DEX: 4, INT: 4, LUK: 4, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000, DemonMark: 1012278}
	stats := NewWriter(0)
	addCharStats(stats, c, time.Unix(0, 0))
	plain := c
	plain.Job = 2300
	plainStats := NewWriter(0)
	addCharStats(plainStats, plain, time.Unix(0, 0))
	// Same SP layout (both extended); the Demon adds one I32 mark.
	if d := len(stats.Packet()) - len(plainStats.Packet()); d != 4 {
		t.Fatalf("demon stat delta %d", d)
	}
	look := NewWriter(0)
	addCharLook(look, c)
	p := look.Packet()
	if r := NewReader(p[len(p)-4:]); r.I32() != 1012278 {
		t.Fatal("avatar look demon mark")
	}
}

func TestRechargeableBundlesCarryTheirSerial(t *testing.T) {
	for id, extra := range map[int]int{2000000: 0, 2070000: 8, 2330000: 8, 3020000: 8, 4006001: 0} {
		w := NewWriter(0)
		addBundleItem(w, domain.InventoryItem{TemplateID: id, Quantity: 5})
		plain := NewWriter(0)
		addBundleItem(plain, domain.InventoryItem{TemplateID: 2000000, Quantity: 5})
		if d := len(w.Packet()) - len(plain.Packet()); d != extra {
			t.Fatalf("item %d serial bytes %d", id, d)
		}
	}
}

func TestWildHuntersCarryJaguarInfo(t *testing.T) {
	base := domain.Character{ID: 7, Name: "HunterQA", Level: 30, Job: 3310, STR: 4, DEX: 4, INT: 4, LUK: 4, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, MapID: 10000}
	other := base
	other.Job = 3210
	hunter, mage := NewWriter(0), NewWriter(0)
	addCharacterInfo(hunter, base, time.Unix(0, 0))
	addCharacterInfo(mage, other, time.Unix(0, 0))
	if d := len(hunter.Packet()) - len(mage.Packet()); d != 21 {
		t.Fatalf("jaguar info bytes %d", d)
	}
}
