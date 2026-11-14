package protocol

import (
	"bytes"
	"testing"

	"maple-go-server/internal/domain"
)

func TestReadGuildOperations(t *testing.T) {
	w := NewRawWriter()
	w.U8(0x06)
	w.I32(12)
	w.I32(34)
	v, err := ReadGuild(NewReader(w.Packet()))
	if err != nil || v.Action != domain.GuildAccept || v.GuildID != 12 || v.TargetID != 34 {
		t.Fatalf("accept %+v %v", v, err)
	}
	w = NewRawWriter()
	w.U8(0x10)
	w.U16(1000)
	w.U8(3)
	w.U16(2001)
	w.U8(7)
	v, err = ReadGuild(NewReader(w.Packet()))
	if err != nil || v.Emblem != (domain.GuildEmblemInfo{BG: 1000, BGColor: 3, Logo: 2001, Color: 7}) {
		t.Fatalf("emblem %+v %v", v, err)
	}
	if v, err = ReadGuild(NewReader([]byte{0x1d, 0xc0, 0x8c, 0x6c, 0x05})); err != nil || v.Action != domain.GuildSkillBuy || v.SkillID != 91000000 {
		t.Fatalf("guild skill purchase %+v %v", v, err)
	}
}

func TestGuildInfoLayout(t *testing.T) {
	defer domain.SetLargeGuildForTest(false)()
	g := domain.GuildSnapshot{ID: 7, Name: "Heroes", Titles: [5]string{"A", "B", "C", "D", "E"}, Capacity: 10, Emblem: domain.GuildEmblemInfo{BG: 1, BGColor: 2, Logo: 3, Color: 4}, Notice: "n", Level: 1,
		Members: []domain.GuildMemberInfo{{CharacterID: 5, Name: "Owner", Level: 30, Job: 100, Rank: 1}}}
	r := NewReader(GuildInfo(g, func(id int64) bool { return id == 5 }))
	if r.U16() != SendGuild || r.U8() != 0x20 || r.U8() != 1 || r.I32() != 7 || r.String() != "Heroes" {
		t.Fatal("header")
	}
	for _, want := range g.Titles {
		if r.String() != want {
			t.Fatal("titles")
		}
	}
	if r.U8() != 1 || r.I32() != 5 || !bytes.HasPrefix(r.Bytes(15), []byte("Owner")) || r.I32() != 100 || r.I32() != 30 || r.I32() != 1 || r.I32() != 1 || r.I32() != 5 || r.I32() != 0 {
		t.Fatal("member")
	}
	if r.I32() != 10 || r.U16() != 1 || r.U8() != 2 || r.U16() != 3 || r.U8() != 4 || r.String() != "n" || r.I32() != 0 || r.I32() != 0 || r.I32() != 0 || r.U8() != 1 || r.U16() != 0 || r.U16() != 0 || r.Remaining() != 0 {
		t.Fatal("tail")
	}
	empty := NewReader(GuildInfo(domain.GuildSnapshot{}, nil))
	if empty.U16() != SendGuild || empty.U8() != 0x20 || empty.U8() != 0 || empty.Remaining() != 0 {
		t.Fatal("no guild")
	}
}

func TestSpawnPlayerGuildMark(t *testing.T) {
	c := domain.Character{ID: 5, Name: "Owner", Level: 10}
	plain := SpawnPlayer(c, 4)
	c.Guild = domain.GuildTag{ID: 7, Name: "Heroes", Emblem: domain.GuildEmblemInfo{BG: 1, BGColor: 2, Logo: 3, Color: 4}}
	marked := SpawnPlayer(c, 4)
	if len(marked) != len(plain)+len("Heroes") || !bytes.Contains(marked, []byte{6, 0, 'H', 'e', 'r', 'o', 'e', 's', 1, 0, 2, 3, 0, 4}) {
		t.Fatal("guild mark missing")
	}
}

func TestNPCTextPromptAndAnswer(t *testing.T) {
	r := NewReader(NPCPrompt(2010009, "text", "name?", nil))
	if r.U16() != SendNPCTalk || r.U8() != 4 || r.I32() != 2010009 || r.U8() != 3 || r.U8() != 0 || r.String() != "name?" || r.I32() != 0 || r.I32() != 0 || r.Remaining() != 0 {
		t.Fatal("text prompt layout")
	}
	w := NewRawWriter()
	w.U8(3)
	w.U8(1)
	w.String("聯盟")
	v, err := ReadNPCResponse(NewReader(w.Packet()))
	if err != nil || v.Kind != "text" || v.Text != "聯盟" || v.Cancel {
		t.Fatalf("text answer %+v %v", v, err)
	}
	v, err = ReadNPCResponse(NewReader([]byte{3, 0}))
	if err != nil || !v.Cancel {
		t.Fatalf("text cancel %+v %v", v, err)
	}
}

func TestMultiChatAndAllianceInfo(t *testing.T) {
	defer domain.SetLargeGuildForTest(false)()
	w := NewRawWriter()
	w.U8(2)
	w.U8(1)
	w.I32(9)
	w.String("hi")
	v, err := ReadMultiChat(NewReader(w.Packet()))
	if err != nil || v.Type != 2 || v.Text != "hi" {
		t.Fatalf("multichat %+v %v", v, err)
	}
	a := domain.AllianceSnapshot{ID: 3, Name: "AL", Titles: [5]string{"a", "b", "c", "d", "e"}, Capacity: 2, Guilds: []domain.GuildSnapshot{{ID: 7}, {ID: 8}}}
	r := NewReader(AllianceInfo(a))
	if r.U16() != SendAlliance || r.U8() != 0x0c || r.U8() != 1 || r.I32() != 3 || r.String() != "AL" {
		t.Fatal("alliance header")
	}
	for range a.Titles {
		_ = r.String()
	}
	if r.U8() != 2 || r.I32() != 7 || r.I32() != 8 || r.I32() != 2 || r.String() != "" || r.Remaining() != 0 {
		t.Fatal("alliance body")
	}
}
