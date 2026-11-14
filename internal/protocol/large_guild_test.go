package protocol

import (
	"fmt"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
)

func largeGuild(id int64, members int) domain.GuildSnapshot {
	g := domain.GuildSnapshot{ID: id, Name: "大公會大公會", Titles: [5]string{"公會長公會長", "副會長副會長", "成員成員成員", "成員成員成員", "成員成員成員"},
		Capacity: members, Notice: strings.Repeat("公", 50), GP: 1, Level: 1}
	for i := range members {
		g.Members = append(g.Members, domain.GuildMemberInfo{CharacterID: id*1000 + int64(i), Name: fmt.Sprintf("Member%06d", i), Level: 200, Job: 112, Rank: 5})
	}
	for i := range 5 {
		g.Skills = append(g.Skills, domain.GuildSkillState{SkillID: 91000000 + i, Level: 1, ExpiresAt: -1, Purchaser: "Purchaser123", Activator: "Activator123"})
	}
	return g
}

func TestLargeGuildRecordCountsMembersInTwoBytes(t *testing.T) {
	defer domain.SetLargeGuildForTest(true)()
	g := largeGuild(7, 300)
	r := NewReader(GuildInfo(g, nil))
	if r.U16() != SendGuild || r.U8() != 0x20 || r.U8() != 1 || r.I32() != 7 {
		t.Fatal("header")
	}
	_ = r.String()
	for range g.Titles {
		_ = r.String()
	}
	if n := r.U16(); n != 300 {
		t.Fatalf("member count %d", n)
	}
	for i := range 300 {
		if r.I32() != int32(g.Members[i].CharacterID) {
			t.Fatalf("member id %d", i)
		}
	}
	r.Bytes(39 * 300)
	if r.I32() != 300 {
		t.Fatal("capacity")
	}

	r = NewReader(GuildCapacityChanged(7, 512))
	if r.U16() != SendGuild || r.U8() != 64 || r.I32() != 7 || r.U16() != 512 || r.Remaining() != 0 {
		t.Fatal("capacity change")
	}
	defer domain.SetLargeGuildForTest(false)()
	r = NewReader(GuildCapacityChanged(7, 200))
	if r.U16() != SendGuild || r.U8() != 64 || r.I32() != 7 || r.U8() != 200 || r.Remaining() != 0 {
		t.Fatal("native capacity change")
	}
}

func TestLargeGuildMultiChatReadsTwoByteCount(t *testing.T) {
	defer domain.SetLargeGuildForTest(true)()
	w := NewRawWriter()
	w.U8(2)
	w.U16(400)
	for i := range 400 {
		w.I32(int32(i + 1))
	}
	w.String("hello")
	v, err := ReadMultiChat(NewReader(w.Packet()))
	if err != nil || v.Type != 2 || len(v.Recipients) != 400 || v.Text != "hello" {
		t.Fatalf("multichat %d %+v %v", len(v.Recipients), v.Text, err)
	}
	w = NewRawWriter()
	w.U8(2)
	w.U16(0)
	w.String("x")
	if _, err = ReadMultiChat(NewReader(w.Packet())); err == nil {
		t.Fatal("no recipients accepted")
	}
}

func TestLargeGuildPacketsFitThePacketLimit(t *testing.T) {
	defer domain.SetLargeGuildForTest(true)()
	limit := domain.MaxGuildCapacityLimit
	a := domain.AllianceSnapshot{ID: 3, Name: "大聯盟大聯盟", Titles: [5]string{"a", "b", "c", "d", "e"}, Capacity: domain.AllianceMaxGuilds, Notice: strings.Repeat("聯", 50)}
	for i := range domain.AllianceMaxGuilds {
		a.Guilds = append(a.Guilds, largeGuild(int64(i+1), limit))
	}
	check := func(name string, p []byte) {
		t.Helper()
		if len(p) > MaxPacketSize {
			t.Fatalf("%s is %d bytes", name, len(p))
		}
	}
	check("guild info", GuildInfo(a.Guilds[0], nil))
	check("guild created", GuildCreated(a.Guilds[0], nil))
	// An alliance is created by two guilds.
	created := a
	created.Guilds = a.Guilds[:2]
	check("alliance created", AllianceCreated(created, nil))
	check("alliance guild joined", AllianceGuildJoined(a, a.Guilds[4], nil))
	check("alliance guild left", AllianceGuildLeft(a, a.Guilds[4], false, nil))
	packets := AllianceGuilds(a, nil)
	if len(packets) != len(a.Guilds) {
		t.Fatalf("%d alliance guild packets", len(packets))
	}
	for i, p := range packets {
		check("alliance guilds", p)
		r := NewReader(p)
		if r.U16() != SendAlliance || r.U8() != 0x0d || r.I32() != 1 || r.I32() != int32(a.Guilds[i].ID) {
			t.Fatalf("alliance guild packet %d", i)
		}
	}
}

func TestClientConfigAnnouncesLargeGuild(t *testing.T) {
	defer domain.SetLargeGuildForTest(true)()
	if ClientConfigFeatures()&ClientFeatureLargeGuild == 0 || ClientFeatureLargeGuild != 1<<8 {
		t.Fatal("large guild bit")
	}
	domain.SetLargeGuild(false)
	if ClientConfigFeatures()&ClientFeatureLargeGuild != 0 {
		t.Fatal("large guild bit while off")
	}
}

func TestLargeBuddyCountsFriendsInTwoBytes(t *testing.T) {
	defer domain.SetLargeBuddyForTest(true)()
	defer domain.SetBuddyCapacityForTest(domain.DefaultBuddyCapacityLimit)()
	list := domain.BuddyList{Capacity: 232}
	for i := range domain.MaxBuddyCapacityLimit {
		list.Entries = append(list.Entries, domain.BuddyEntry{CharacterID: int64(i + 1), Name: fmt.Sprintf("Friend%06d", i), Group: "群組未指定", Channel: 1})
	}
	p := BuddyListPacket(list, 7)
	if p == nil || len(p) > MaxPacketSize {
		t.Fatalf("full list is %d bytes", len(p))
	}
	r := NewReader(p)
	if r.U16() != SendBuddy || r.U8() != 7 || int(r.U16()) != domain.MaxBuddyCapacityLimit || r.I32() != 1 {
		t.Fatal("list header")
	}
	if BuddyListPacket(domain.BuddyList{Entries: make([]domain.BuddyEntry, domain.MaxBuddyCapacityLimit+1)}, 7) != nil {
		t.Fatal("list above the limit")
	}
	r = NewReader(BuddyCapacity(512))
	if r.U16() != SendBuddy || r.U8() != 21 || r.U16() != 512 || r.Remaining() != 0 {
		t.Fatal("capacity")
	}
	if domain.EffectiveBuddyCapacity(232) != 512 || domain.EffectiveBuddyCapacity(800) != 800 {
		t.Fatal("effective capacity")
	}
	domain.SetLargeBuddy(false)
	if domain.EffectiveBuddyCapacity(232) != 255 || BuddyCapacity(300) != nil {
		t.Fatal("native ceiling")
	}
	if ClientConfigFeatures()&ClientFeatureLargeBuddy != 0 || ClientFeatureLargeBuddy != 1<<9 {
		t.Fatal("large buddy bit")
	}
}
