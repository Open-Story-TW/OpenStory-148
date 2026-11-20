package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrGuildFull          = errors.New("guild full")
	ErrGuildInvitePending = errors.New("guild invitation already pending")
)

const (
	GuildOfficeMapID    = 200000301 // Legacy 英雄之殿 guild office
	GuildCreateCost     = 5000000
	GuildEmblemCost     = 1500000
	GuildInviteLifetime = 20 * time.Minute
	GuildMaster         = 1
	GuildJrMaster       = 2
	GuildMember         = 5
	GuildCapacityMesos  = 500000
	GuildCapacityGP     = 25000
	GuildSkillGP        = 30
	GuildHourlyGP       = 20
	AllianceCapacityFee = 10000000
	AllianceMaxGuilds   = 5
)

const (
	GuildSupportQuestID = 26000
	GuildSupportNPCID   = 2010010
	GuildSupportSkillID = 91000006
	GuildSupportPotion  = 2002037
)

// Legacy GameConstants.guildexp; level i needs guildexp[i] GP.
var guildExp = [...]int{0, 20000, 160000, 540000, 1280000, 2500000, 4320000, 6860000, 10240000, 14580000}

// GuildLevelFor mirrors MapleGuild.calculateLevel.
func GuildLevelFor(gp int) int {
	for i := 1; i < len(guildExp); i++ {
		if gp < guildExp[i] {
			return i
		}
	}
	return len(guildExp)
}

type GuildAction string

const (
	GuildCreate     GuildAction = "create"
	GuildInvite     GuildAction = "invite"
	GuildAccept     GuildAction = "accept"
	GuildDeny       GuildAction = "deny"
	GuildLeave      GuildAction = "leave"
	GuildExpel      GuildAction = "expel"
	GuildTitles     GuildAction = "titles"
	GuildChangeRank GuildAction = "rank"
	GuildEmblem     GuildAction = "emblem"
	GuildNotice     GuildAction = "notice"
	GuildLeader     GuildAction = "leader"
	GuildDisband    GuildAction = "disband"
	GuildCapacity   GuildAction = "capacity"
	GuildSkillBuy   GuildAction = "skill_buy"
	GuildSkillRenew GuildAction = "skill_renew"
	GuildGainGP     GuildAction = "gain_gp"
)

type GuildSkillState struct {
	SkillID   int    `json:"skill_id"`
	Level     int    `json:"level"`
	ExpiresAt int64  `json:"expires_at"` // Unix ms; -1 permanent
	Purchaser string `json:"purchaser"`
	Activator string `json:"activator"`
}

type GuildMemberInfo struct {
	CharacterID  int64  `json:"character_id"`
	Name         string `json:"name"`
	Level        int    `json:"level"`
	Job          int    `json:"job"`
	Rank         int    `json:"rank"`
	Contribution int    `json:"contribution"`
	AllianceRank int    `json:"alliance_rank"`
}

type GuildEmblemInfo struct {
	BG      int `json:"bg"`
	BGColor int `json:"bg_color"`
	Logo    int `json:"logo"`
	Color   int `json:"color"`
}

type GuildSnapshot struct {
	ID       int64             `json:"id"`
	WorldID  int               `json:"world_id"`
	Name     string            `json:"name"`
	LeaderID int64             `json:"leader_id"`
	Titles   [5]string         `json:"titles"`
	Capacity int               `json:"capacity"`
	Emblem   GuildEmblemInfo   `json:"emblem"`
	Notice   string            `json:"notice"`
	GP       int               `json:"gp"`
	Level    int               `json:"level"`
	Revision int64             `json:"revision"`
	Members  []GuildMemberInfo `json:"members"`
	Skills   []GuildSkillState `json:"skills,omitempty"`
	Alliance int64             `json:"alliance_id,omitempty"`
}

func (g GuildSnapshot) Member(id int64) (GuildMemberInfo, bool) {
	for _, m := range g.Members {
		if m.CharacterID == id {
			return m, true
		}
	}
	return GuildMemberInfo{}, false
}

// GuildTag is the guild identity shown on a character (SPAWN_PLAYER etc.).
type GuildTag struct {
	ID     int64           `json:"id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Rank   int             `json:"rank,omitempty"`
	Emblem GuildEmblemInfo `json:"emblem,omitzero"`
}

func (g GuildSnapshot) Tag(characterID int64) GuildTag {
	m, ok := g.Member(characterID)
	if !ok || g.ID == 0 {
		return GuildTag{}
	}
	return GuildTag{ID: g.ID, Name: g.Name, Rank: m.Rank, Emblem: g.Emblem}
}

type GuildInvitation struct {
	GuildID      int64     `json:"guild_id"`
	GuildName    string    `json:"guild_name"`
	InviterID    int64     `json:"inviter_id"`
	InviterName  string    `json:"inviter_name"`
	InviterLevel int       `json:"inviter_level"`
	InviterJob   int       `json:"inviter_job"`
	TargetID     int64     `json:"target_id"`
	TargetName   string    `json:"target_name"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type GuildRequest struct {
	Session     CharacterSession
	OperationID string
	Action      GuildAction
	Name        string // create: guild name; invite/deny: character name
	TargetID    int64
	GuildID     int64
	Rank        int
	Titles      [5]string
	Emblem      GuildEmblemInfo
	Notice      string
	SkillID     int
	UseGP       bool
	Amount      int    // gain_gp
	Period      string // gain_gp idempotency period
}

type GuildMutation struct {
	Action     GuildAction      `json:"action"`
	Before     GuildSnapshot    `json:"before"`
	After      GuildSnapshot    `json:"after"`
	TargetID   int64            `json:"target_id,omitempty"`
	TargetName string           `json:"target_name,omitempty"`
	Invitation *GuildInvitation `json:"invitation,omitempty"`
	Disbanded  bool             `json:"disbanded,omitempty"`
	Skill      *GuildSkillState `json:"skill,omitempty"`
	Character  *Character       `json:"character,omitempty"`
}

type GuildRepository interface {
	ReadGuild(context.Context, CharacterSession) (GuildSnapshot, error)
	MutateGuild(context.Context, GuildRequest) (GuildMutation, error)
}
