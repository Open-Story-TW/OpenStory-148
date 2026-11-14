// Alliance layouts derived from Legacy/OdinMS AllianceHandler and
// CWvsContext.AlliancePacket (AGPLv3).
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import "maple-go-server/internal/domain"

const (
	RecvAlliance     uint16 = 0xf1
	RecvAllianceDeny uint16 = 0xf2
	SendAlliance     uint16 = 0x55
)

// ReadAlliance maps ALLIANCE_OPERATION; "info" is op 1 (load).
func ReadAlliance(r *Reader) (domain.AllianceRequest, error) {
	var v domain.AllianceRequest
	switch op := r.U8(); op {
	case 1:
		v.Action = "info"
	case 2:
		v.Action = domain.AllianceLeave
	case 3:
		v.Action, v.Name = domain.AllianceInvite, r.String()
	case 4:
		v.Action = domain.AllianceAccept
		if r.Remaining() >= 4 {
			r.I32()
		}
		if r.Remaining() >= 2 {
			_ = r.String()
		}
	case 6:
		v.Action, v.GuildID = domain.AllianceExpel, int64(r.I32())
		if r.Remaining() >= 4 {
			r.I32()
		}
	case 7:
		v.Action, v.TargetID = domain.AllianceLeader, int64(r.I32())
	case 8:
		v.Action = domain.AllianceTitles
		for i := range v.Titles {
			v.Titles[i] = r.String()
		}
	case 9:
		// The v148 職位+/職位- buttons send 1 raise, 0 lower (0x8B1BCE).
		v.Action, v.TargetID = domain.AllianceRank, int64(r.I32())
		switch r.U8() {
		case 1:
			v.RankStep = 1
		case 0:
			v.RankStep = -1
		default:
			return v, ErrMalformed
		}
	case 10:
		v.Action, v.Notice = domain.AllianceNotice, r.String()
	case 22:
		v.Action = domain.AllianceDeny
	default:
		return v, ErrMalformed
	}
	if r.Err() != nil {
		return domain.AllianceRequest{}, ErrMalformed
	}
	return v, nil
}

func addAllianceInfo(w *Writer, a domain.AllianceSnapshot) {
	w.I32(int32(a.ID))
	w.String(a.Name)
	for _, t := range a.Titles {
		w.String(t)
	}
	w.U8(byte(len(a.Guilds)))
	for _, g := range a.Guilds {
		w.I32(int32(g.ID))
	}
	w.I32(int32(a.Capacity))
	w.String(a.Notice)
}

// AllianceInfo is op 0x0C; an empty snapshot clears the alliance.
func AllianceInfo(a domain.AllianceSnapshot) []byte {
	w := NewWriter(SendAlliance)
	w.U8(0x0c)
	if a.ID == 0 {
		w.U8(0)
		return w.Packet()
	}
	w.U8(1)
	addAllianceInfo(w, a)
	return w.Packet()
}

// AllianceGuilds is op 0x0D, one packet per allied guild record. The client
// adds or replaces each record by guild id (0x9EA3AB), so splitting keeps
// five large guilds inside the 64 KiB packet limit.
func AllianceGuilds(a domain.AllianceSnapshot, online func(int64) bool) [][]byte {
	out := make([][]byte, 0, len(a.Guilds))
	for _, g := range a.Guilds {
		w := NewWriter(SendAlliance)
		w.U8(0x0d)
		w.I32(1)
		addGuildInfo(w, g, online)
		out = append(out, w.Packet())
	}
	return out
}

func AllianceCreated(a domain.AllianceSnapshot, online func(int64) bool) []byte {
	w := NewWriter(SendAlliance)
	w.U8(15)
	addAllianceInfo(w, a)
	for _, g := range a.Guilds {
		addGuildInfo(w, g, online)
	}
	return w.Packet()
}

func AllianceGuildJoined(a domain.AllianceSnapshot, g domain.GuildSnapshot, online func(int64) bool) []byte {
	w := NewWriter(SendAlliance)
	w.U8(18)
	addAllianceInfo(w, a)
	w.I32(int32(g.ID))
	addGuildInfo(w, g, online)
	w.U8(0)
	return w.Packet()
}

func AllianceGuildLeft(a domain.AllianceSnapshot, g domain.GuildSnapshot, expelled bool, online func(int64) bool) []byte {
	w := NewWriter(SendAlliance)
	w.U8(16)
	addAllianceInfo(w, a)
	addGuildInfo(w, g, online)
	w.Bool(expelled)
	return w.Packet()
}

func AllianceInvite(guildID int64, inviter, alliance string) []byte {
	w := NewWriter(SendAlliance)
	w.U8(3)
	w.I32(int32(guildID))
	w.String(inviter)
	w.String(alliance)
	return w.Packet()
}

func AllianceUpdate(a domain.AllianceSnapshot) []byte {
	w := NewWriter(SendAlliance)
	w.U8(0x17)
	addAllianceInfo(w, a)
	return w.Packet()
}

func AllianceLeaderChanged(allianceID, oldLeader, newLeader int64) []byte {
	w := NewWriter(SendAlliance)
	w.U8(25)
	w.I32(int32(allianceID))
	w.I32(int32(oldLeader))
	w.I32(int32(newLeader))
	return w.Packet()
}

// AllianceTitlesChanged is alliance op 26 on v148 (0x9EA438 reads the
// alliance id and five names). Legacy sent it as GUILD_OPERATION 26, which the
// v148 guild handler takes for a failed guild invitation.
func AllianceTitlesChanged(allianceID int64, titles [5]string) []byte {
	w := NewWriter(SendAlliance)
	w.U8(26)
	w.I32(int32(allianceID))
	for _, t := range titles {
		w.String(t)
	}
	return w.Packet()
}

func AllianceRankChanged(characterID int64, rank int) []byte {
	w := NewWriter(SendAlliance)
	w.U8(27)
	w.I32(int32(characterID))
	w.U8(byte(rank))
	return w.Packet()
}

func AllianceNoticeChanged(allianceID int64, notice string) []byte {
	w := NewWriter(SendAlliance)
	w.U8(28)
	w.I32(int32(allianceID))
	w.String(notice)
	return w.Packet()
}

func AllianceDisbanded(allianceID int64) []byte {
	w := NewWriter(SendAlliance)
	w.U8(29)
	w.I32(int32(allianceID))
	return w.Packet()
}

func AllianceMemberOnline(allianceID, guildID, characterID int64, online bool) []byte {
	w := NewWriter(SendAlliance)
	w.U8(0x0e)
	w.I32(int32(allianceID))
	w.I32(int32(guildID))
	w.I32(int32(characterID))
	w.Bool(online)
	return w.Packet()
}

// AllianceMembership is op 1 (changeAlliance): per-member alliance ranks.
func AllianceMembership(a domain.AllianceSnapshot, in bool) []byte {
	w := NewWriter(SendAlliance)
	w.U8(1)
	w.Bool(in)
	if in {
		w.I32(int32(a.ID))
	} else {
		w.I32(0)
	}
	w.U8(byte(len(a.Guilds)))
	for _, g := range a.Guilds {
		w.I32(int32(g.ID))
		w.I32(int32(len(g.Members)))
		for _, m := range g.Members {
			w.I32(int32(m.CharacterID))
			if in {
				w.U8(byte(allianceRank(m)))
			} else {
				w.U8(0)
			}
		}
	}
	return w.Packet()
}
