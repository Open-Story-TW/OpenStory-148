// Guild layouts derived from Legacy/OdinMS GuildHandler and
// CWvsContext.GuildPacket (AGPLv3).
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import (
	"time"

	"maple-go-server/internal/domain"
)

const (
	RecvGuild          uint16 = 0xe0
	RecvGuildDeny      uint16 = 0xe1
	SendGuild          uint16 = 0x54
	SendLoadGuildName  uint16 = 0x148
	SendLoadGuildIcon  uint16 = 0x149
	SendShowStatusInfo uint16 = 0x36
	RecvMultiChat      uint16 = 0xd7
	SendMultiChat      uint16 = 0xdc
)

// MultiChat is party(1), guild(2) or alliance(3) chat.
type MultiChat struct {
	Type       int
	Recipients []int64
	Text       string
}

// maxChatRecipients bounds the client's recipient list: every other member
// of a full alliance.
const maxChatRecipients = domain.AllianceMaxGuilds * domain.MaxGuildCapacityLimit

// guildCount writes a guild member count: two bytes under the large guild
// contract, one byte natively.
func guildCount(w *Writer, n int) {
	if domain.LargeGuildEnabled() {
		w.U16(uint16(n))
		return
	}
	w.U8(byte(n))
}

// ReadMultiChat reads MULTICHAT. The recipient count is two bytes under the
// large guild contract.
// The recipients themselves are ignored: delivery follows durable membership.
func ReadMultiChat(r *Reader) (MultiChat, error) {
	v := MultiChat{Type: int(r.U8())}
	var n int
	if domain.LargeGuildEnabled() {
		n = int(r.U16())
	} else {
		n = int(r.U8())
	}
	if n < 1 || n > maxChatRecipients {
		return MultiChat{}, ErrMalformed
	}
	for i := 0; i < n; i++ {
		v.Recipients = append(v.Recipients, int64(r.I32()))
	}
	v.Text = r.String()
	if r.Err() != nil || r.Remaining() != 0 || v.Type < 0 || v.Type > 3 || len(v.Text) < 1 || len(v.Text) > 127 {
		return MultiChat{}, ErrMalformed
	}
	return v, nil
}

func MultiChatText(mode int, name, text string) []byte {
	w := NewWriter(SendMultiChat)
	w.U8(byte(mode))
	w.String(name)
	w.String(text)
	return w.Packet()
}

// Native GUILD_OPERATION result codes used by Legacy.
const (
	GuildMsgNameTaken    byte = 34
	GuildMsgProblem      byte = 37
	GuildMsgFull         byte = 47
	GuildMsgNotFound     byte = 48
	GuildMsgNotInGuild   byte = 51
	GuildMsgBusy         byte = 60
	GuildMsgAlreadyGuild byte = 46
)

// ReadGuild maps a GUILD_OPERATION body to a request without a session.
func ReadGuild(r *Reader) (domain.GuildRequest, error) {
	var v domain.GuildRequest
	mode := r.U8()
	switch mode {
	case 0x00:
		v.Action = "info"
	case 0x02:
		v.Action, v.Name = domain.GuildCreate, r.String()
	case 0x05:
		v.Action, v.Name = domain.GuildInvite, r.String()
	case 0x06:
		v.Action, v.GuildID, v.TargetID = domain.GuildAccept, int64(r.I32()), int64(r.I32())
	case 0x07:
		v.Action, v.TargetID, v.Name = domain.GuildLeave, int64(r.I32()), r.String()
	case 0x08:
		v.Action, v.TargetID, v.Name = domain.GuildExpel, int64(r.I32()), r.String()
	case 0x0e:
		v.Action = domain.GuildTitles
		for i := range v.Titles {
			v.Titles[i] = r.String()
		}
	case 0x0f:
		v.Action, v.TargetID, v.Rank = domain.GuildChangeRank, int64(r.I32()), int(r.U8())
	case 0x10:
		v.Action = domain.GuildEmblem
		v.Emblem = domain.GuildEmblemInfo{BG: int(r.U16()), BGColor: int(r.U8()), Logo: int(r.U16()), Color: int(r.U8())}
	case 0x11:
		v.Action, v.Notice = domain.GuildNotice, r.String()
	case 0x1d:
		v.Action, v.SkillID = domain.GuildSkillBuy, int(r.I32())
	case 0x1e:
		v.Action, v.SkillID = domain.GuildSkillRenew, int(r.I32())
	case 0x1f:
		v.Action, v.TargetID = domain.GuildLeader, int64(r.I32())
	default:
		return v, ErrMalformed
	}
	if r.Err() != nil {
		return domain.GuildRequest{}, ErrMalformed
	}
	return v, nil
}

// ReadGuildDeny returns the native reason (59 invites disabled, 60 busy) and
// the inviter name; the client appends its own name, which is ignored.
func ReadGuildDeny(r *Reader) (int, string, error) {
	reason := int(r.U8())
	name := r.String()
	if r.Err() != nil {
		return 0, "", ErrMalformed
	}
	return reason, name, nil
}

func addGuildMark(w *Writer, t domain.GuildTag) {
	if t.ID == 0 {
		w.String("")
		w.U16(0)
		w.U8(0)
		w.U16(0)
		w.U8(0)
		return
	}
	w.String(t.Name)
	addGuildEmblem(w, t.Emblem)
}

func addGuildEmblem(w *Writer, e domain.GuildEmblemInfo) {
	w.U16(uint16(e.BG))
	w.U8(byte(e.BGColor))
	w.U16(uint16(e.Logo))
	w.U8(byte(e.Color))
}

// addGuildInfo is GuildPacket.getGuildInfo; online is presence per member.
func addGuildInfo(w *Writer, g domain.GuildSnapshot, online func(int64) bool) {
	w.I32(int32(g.ID))
	w.String(g.Name)
	for _, t := range g.Titles {
		w.String(t)
	}
	guildCount(w, len(g.Members))
	for _, m := range g.Members {
		w.I32(int32(m.CharacterID))
	}
	for _, m := range g.Members {
		w.FixedString(m.Name, 15)
		w.I32(int32(m.Job))
		w.I32(int32(m.Level))
		w.I32(int32(m.Rank))
		w.I32(boolInt(online != nil && online(m.CharacterID)))
		w.I32(int32(allianceRank(m)))
		w.I32(int32(m.Contribution))
	}
	w.I32(int32(g.Capacity))
	addGuildEmblem(w, g.Emblem)
	w.String(g.Notice)
	w.I32(int32(g.GP))
	w.I32(int32(g.GP))
	w.I32(int32(g.Alliance))
	w.U8(byte(domain.GuildLevelFor(g.GP)))
	w.U16(0)
	w.U16(uint16(len(g.Skills)))
	for _, k := range g.Skills {
		addGuildSkill(w, k)
	}
}

func allianceRank(m domain.GuildMemberInfo) int {
	if m.AllianceRank < 1 || m.AllianceRank > 5 {
		return 5
	}
	return m.AllianceRank
}

func skillTime(ms int64) int64 {
	if ms <= 0 {
		return maxTime
	}
	return mapleTime(time.UnixMilli(ms))
}

func addGuildSkill(w *Writer, k domain.GuildSkillState) {
	w.I32(int32(k.SkillID))
	w.U16(uint16(k.Level))
	w.I64(skillTime(k.ExpiresAt))
	w.String(k.Purchaser)
	w.String(k.Activator)
}

// GuildSkillChanged is mode 85 (guildSkillPurchased).
func GuildSkillChanged(guildID int64, k domain.GuildSkillState) []byte {
	w := NewWriter(SendGuild)
	w.U8(85)
	w.I32(int32(guildID))
	addGuildSkill(w, k)
	return w.Packet()
}

func GuildGP(guildID int64, gp int) []byte {
	w := NewWriter(SendGuild)
	w.U8(79)
	w.I32(int32(guildID))
	w.I32(int32(gp))
	w.I32(int32(domain.GuildLevelFor(gp)))
	return w.Packet()
}

func GuildContribution(guildID, characterID int64, contribution int) []byte {
	w := NewWriter(SendGuild)
	w.U8(72)
	w.I32(int32(guildID))
	w.I32(int32(characterID))
	w.I32(int32(contribution))
	return w.Packet()
}

// GuildCapacityChanged is mode 64; the capacity takes the member count's
// width.
func GuildCapacityChanged(guildID int64, capacity int) []byte {
	w := NewWriter(SendGuild)
	w.U8(64)
	w.I32(int32(guildID))
	guildCount(w, capacity)
	return w.Packet()
}

// GPGainMessage is SHOW_STATUS_INFO 7 (guild) or 8 (own contribution).
func GPGainMessage(amount int, contribution bool) []byte {
	w := NewWriter(SendShowStatusInfo)
	if contribution {
		w.U8(8)
	} else {
		w.U8(7)
	}
	w.I32(int32(amount))
	return w.Packet()
}

// GuildInfo is mode 0x20: no guild, or the full guild record.
func GuildInfo(g domain.GuildSnapshot, online func(int64) bool) []byte {
	w := NewWriter(SendGuild)
	w.U8(0x20)
	if g.ID == 0 {
		w.U8(0)
		return w.Packet()
	}
	w.U8(1)
	addGuildInfo(w, g, online)
	return w.Packet()
}

// GuildCreated is mode 38, the guild office congratulation plus record.
func GuildCreated(g domain.GuildSnapshot, online func(int64) bool) []byte {
	w := NewWriter(SendGuild)
	w.U8(38)
	addGuildInfo(w, g, online)
	return w.Packet()
}

func GuildInviteNotice(inv domain.GuildInvitation) []byte {
	w := NewWriter(SendGuild)
	w.U8(5)
	w.I32(int32(inv.GuildID))
	w.String(inv.InviterName)
	w.I32(int32(inv.InviterLevel))
	w.I32(int32(inv.InviterJob))
	w.I32(0)
	return w.Packet()
}

func GuildMemberJoined(guildID int64, m domain.GuildMemberInfo, online bool) []byte {
	w := NewWriter(SendGuild)
	w.U8(45)
	w.I32(int32(guildID))
	w.I32(int32(m.CharacterID))
	w.FixedString(m.Name, 15)
	w.I32(int32(m.Job))
	w.I32(int32(m.Level))
	w.I32(int32(m.Rank))
	w.I32(boolInt(online))
	w.I32(5)
	w.I32(int32(m.Contribution))
	return w.Packet()
}

func GuildMemberLeft(guildID, characterID int64, name string, expelled bool) []byte {
	w := NewWriter(SendGuild)
	if expelled {
		w.U8(53)
	} else {
		w.U8(50)
	}
	w.I32(int32(guildID))
	w.I32(int32(characterID))
	w.String(name)
	return w.Packet()
}

func GuildDisbanded(guildID int64) []byte {
	w := NewWriter(SendGuild)
	w.U8(56)
	w.I32(int32(guildID))
	w.U8(1)
	return w.Packet()
}

func GuildRankChanged(guildID, characterID int64, rank int) []byte {
	w := NewWriter(SendGuild)
	w.U8(70)
	w.I32(int32(guildID))
	w.I32(int32(characterID))
	w.U8(byte(rank))
	return w.Packet()
}

func GuildTitlesChanged(guildID int64, titles [5]string) []byte {
	w := NewWriter(SendGuild)
	w.U8(68)
	w.I32(int32(guildID))
	for _, t := range titles {
		w.String(t)
	}
	return w.Packet()
}

func GuildEmblemChanged(guildID int64, e domain.GuildEmblemInfo) []byte {
	w := NewWriter(SendGuild)
	w.U8(73)
	w.I32(int32(guildID))
	addGuildEmblem(w, e)
	return w.Packet()
}

func GuildNoticeChanged(guildID int64, notice string) []byte {
	w := NewWriter(SendGuild)
	w.U8(75)
	w.I32(int32(guildID))
	w.String(notice)
	return w.Packet()
}

func GuildMemberOnline(guildID, characterID int64, online bool) []byte {
	w := NewWriter(SendGuild)
	w.U8(67)
	w.I32(int32(guildID))
	w.I32(int32(characterID))
	w.Bool(online)
	return w.Packet()
}

func GuildLeaderChanged(guildID, oldLeader, newLeader int64) []byte {
	w := NewWriter(SendGuild)
	w.U8(89)
	w.I32(int32(guildID))
	w.I32(int32(oldLeader))
	w.I32(int32(newLeader))
	w.U8(1)
	w.I32(0)
	return w.Packet()
}

func GuildDenied(name string) []byte {
	w := NewWriter(SendGuild)
	w.U8(61)
	w.String(name)
	return w.Packet()
}

// GuildMessage is genericGuildMessage for codes without a string tail.
func GuildMessage(code byte) []byte {
	w := NewWriter(SendGuild)
	w.U8(code)
	if code == 60 || code == 59 {
		w.String("")
	}
	return w.Packet()
}

func LoadGuildName(characterID int64, t domain.GuildTag) []byte {
	w := NewWriter(SendLoadGuildName)
	w.I32(int32(characterID))
	if t.ID == 0 {
		w.U16(0)
	} else {
		w.String(t.Name)
	}
	return w.Packet()
}

func LoadGuildIcon(characterID int64, t domain.GuildTag) []byte {
	w := NewWriter(SendLoadGuildIcon)
	w.I32(int32(characterID))
	if t.ID == 0 {
		w.Zero(6)
	} else {
		addGuildEmblem(w, t.Emblem)
	}
	return w.Packet()
}

func boolInt(v bool) int32 {
	if v {
		return 1
	}
	return 0
}
