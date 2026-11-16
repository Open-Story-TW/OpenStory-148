package domain

import (
	"context"
	"strings"
	"time"
)

const QAGMRole = "qa_skill_tester"
const QAGMRank = 6

// QAGMPermanentExpiry is the expiry stored for a standing grant. It is a real
// timestamp so every "expires_at is in the future" check keeps working.
var QAGMPermanentExpiry = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// QAGMGrantRequest is an operator capability. Never expose it through player
// chat, scripts, character creation, or an unauthenticated RPC facade.
type QAGMGrantRequest struct {
	OperationID string    `json:"operation_id"`
	Actor       string    `json:"-"`
	Action      string    `json:"action"`
	AccountID   int64     `json:"account_id"`
	CharacterID int64     `json:"character_id"`
	WorldID     int       `json:"world_id"`
	Role        string    `json:"role"`
	ExpiresAt   time.Time `json:"expires_at"`
	Reason      string    `json:"reason"`
	// Permanent asks for a standing grant with no 24-hour limit. Only the web
	// admin sets it; it is deliberately not readable from a request body.
	Permanent bool `json:"-"`
}
type QAGMGrant struct {
	AccountID   int64     `json:"account_id"`
	CharacterID int64     `json:"character_id"`
	WorldID     int       `json:"world_id"`
	Role        string    `json:"role"`
	ExpiresAt   time.Time `json:"expires_at"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
}
type QAGMGrantResult struct {
	Grant    QAGMGrant `json:"grant"`
	Replayed bool      `json:"replayed"`
}
type GMCommandRequest struct {
	Session     CharacterSession `json:"-"`
	OperationID string           `json:"operation_id"`
	Name        string           `json:"name"`
	Args        []int64          `json:"args"`
	RuntimeID   string           `json:"runtime_id,omitempty"`
	// Text is the one free-text argument: a character name or a notice.
	Text string `json:"text,omitempty"`
}

// Limits of the standing GM commands.
const (
	GMNoticeMax = 200     // bytes of one !notice
	GMNameMax   = 32      // bytes of a character name argument
	GMWalletMax = 1000000 // one !cash or !points grant
)

// GMBan is the outcome of !ban or !unban: the account behind a character.
type GMBan struct {
	AccountID    int64
	Account      string
	CharacterIDs []int64
	Until        time.Time
}

// GMBanDaysMax bounds a timed ban; without a day count a ban does not end.
const GMBanDaysMax = 3650

// GMStandingCommand reports the commands that act on other players or on the
// whole world. They need a standing grant (no expiry, given in the web admin);
// a timed QA grant keeps the self-only test commands.
func GMStandingCommand(name string) bool {
	switch name {
	case "map", "goto", "warphere", "notice", "dc", "npc", "cash", "points",
		"ban", "unban", "banip", "unbanip", "rate", "killall", "cleardrops", "hide", "god", "oxquiz":
		return true
	}
	return false
}

// ValidGMStandingCommand checks the argument shape of a standing GM command.
func ValidGMStandingCommand(name string, args []int64, text string) bool {
	for _, c := range text {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	one := func(low, high int64) bool { return text == "" && len(args) == 1 && args[0] >= low && args[0] <= high }
	switch name {
	case "map":
		return one(0, 999999998)
	case "npc":
		return one(1, 9999999)
	case "cash", "points":
		return one(1, GMWalletMax)
	case "hide", "god":
		return one(0, 1)
	case "oxquiz":
		return one(1, 3)
	case "killall", "cleardrops":
		return text == "" && len(args) == 0
	case "rate": // kind (1 EXP, 2 mesos, 3 drops), multiplier
		return text == "" && len(args) == 2 && args[0] >= 1 && args[0] <= 3 && args[1] >= 1 && args[1] <= MaxRate
	case "ban": // name, then an optional number of days
		if len(args) == 1 && (args[0] < 1 || args[0] > GMBanDaysMax) || len(args) > 1 {
			return false
		}
		return text != "" && len(text) <= GMNameMax && !strings.ContainsAny(text, " \t")
	case "goto", "warphere", "dc", "unban", "banip", "unbanip":
		return len(args) == 0 && text != "" && len(text) <= GMNameMax && !strings.ContainsAny(text, " \t")
	case "notice":
		return len(args) == 0 && strings.TrimSpace(text) != "" && len(text) <= GMNoticeMax
	}
	return false
}

type GMCommandResult struct {
	Character  Character `json:"character"`
	Name       string    `json:"name"`
	Args       []int64   `json:"args"`
	Text       string    `json:"text,omitempty"`      // the request's text, kept for the audit
	Standing   bool      `json:"standing,omitempty"`  // the grant has no expiry
	SkillIDs   []int     `json:"skill_ids,omitempty"` // skills changed by !maxskills
	Changed    bool      `json:"changed"`
	Replayed   bool      `json:"replayed"`
	Denied     bool      `json:"denied"`
	Reason     string    `json:"reason,omitempty"`
	Detail     string    `json:"detail,omitempty"` // why a QA setup command was refused
	SkillID    int       `json:"skill_id,omitempty"`
	MapChanged bool      `json:"map_changed"`
	// CashExpiry audits !cashexpire: an account locker record, not character state.
	CashExpiry *GMCashExpiry `json:"cash_expiry,omitempty"`
	// PetExpiry audits !petexpire: one owned CASH-bag pet's lifetime.
	PetExpiry *GMCashExpiry `json:"pet_expiry,omitempty"`
	// Inventory lists bag changes made by !petfood.
	Inventory []InventoryChange   `json:"inventory,omitempty"`
	QAMobs    *QAMobAuthorization `json:"qa_mobs,omitempty"`
}

// GMCashExpiry records one QA locker expiry rewrite in database-clock epoch ms.
type GMCashExpiry struct {
	CashID            int64 `json:"cash_id"`
	TemplateID        int   `json:"template_id"`
	PreviousExpiresAt int64 `json:"previous_expires_at"`
	ExpiresAt         int64 `json:"expires_at"`
}

// QACashExpiryMaxSeconds bounds !cashexpire to a short-lived QA window.
const QACashExpiryMaxSeconds = 30 * 24 * 60 * 60

// QAPetFoodItem is the ordinary pet food (Legacy PetHandler accepts 212xxxx).
const (
	QAPetFoodItem  = 2120000
	QAPetFoodMax   = 100
	QAItemMax      = 1000 // one !item grant; equipment is granted singly
	QAPetExpiryMax = 24 * 60 * 60
	QAMesosMax     = 50000000
)

type GMRepository interface {
	AuthorizeQAGM(context.Context, QAGMGrantRequest) (QAGMGrantResult, error)
	ExecuteGMCommand(context.Context, GMCommandRequest) (GMCommandResult, error)
	ResolveGMCommand(context.Context, GMCommandRequest) (GMCommandResult, bool, error)
}
