package domain

import "context"

type AllianceAction string

const (
	AllianceCreate   AllianceAction = "create"
	AllianceInvite   AllianceAction = "invite"
	AllianceAccept   AllianceAction = "accept"
	AllianceDeny     AllianceAction = "deny"
	AllianceLeave    AllianceAction = "leave"
	AllianceExpel    AllianceAction = "expel"
	AllianceLeader   AllianceAction = "leader"
	AllianceTitles   AllianceAction = "titles"
	AllianceRank     AllianceAction = "rank"
	AllianceNotice   AllianceAction = "notice"
	AllianceCapacity AllianceAction = "capacity"
	AllianceDisband  AllianceAction = "disband"
)

type AllianceSnapshot struct {
	ID       int64           `json:"id"`
	WorldID  int             `json:"world_id"`
	Name     string          `json:"name"`
	LeaderID int64           `json:"leader_id"`
	Titles   [5]string       `json:"titles"`
	Capacity int             `json:"capacity"`
	Notice   string          `json:"notice"`
	Revision int64           `json:"revision"`
	Guilds   []GuildSnapshot `json:"guilds"`
}

// Members returns every member of every allied guild.
func (a AllianceSnapshot) Members() []GuildMemberInfo {
	var out []GuildMemberInfo
	for _, g := range a.Guilds {
		out = append(out, g.Members...)
	}
	return out
}

type AllianceRequest struct {
	Session     CharacterSession
	OperationID string
	Action      AllianceAction
	Name        string // create: alliance name; invite: guild master name
	PartnerID   int64  // create: the other guild master in the party
	GuildID     int64  // expel
	TargetID    int64  // leader/rank
	// Rank is an absolute alliance rank 3-5, or 0 with RankStep set: the
	// v148 alliance tab (0x8A2DCA) sends raise (+1) or lower (-1) only.
	Rank     int
	RankStep int
	Titles   [5]string
	Notice   string
}

type AllianceMutation struct {
	Action      AllianceAction   `json:"action"`
	Before      AllianceSnapshot `json:"before"`
	After       AllianceSnapshot `json:"after"`
	GuildID     int64            `json:"guild_id,omitempty"`   // joined/left guild
	Guild       GuildSnapshot    `json:"guild"`                // that guild after the change
	InviteeID   int64            `json:"invitee_id,omitempty"` // invited guild master
	InviterName string           `json:"inviter_name,omitempty"`
	Expelled    bool             `json:"expelled,omitempty"`
	Disbanded   bool             `json:"disbanded,omitempty"`
	Rank        int              `json:"rank,omitempty"` // rank: the target's new alliance rank
	Character   *Character       `json:"character,omitempty"`
}

type AllianceRepository interface {
	ReadAlliance(context.Context, CharacterSession) (AllianceSnapshot, error)
	MutateAlliance(context.Context, AllianceRequest) (AllianceMutation, error)
}
