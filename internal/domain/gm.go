package domain

import (
	"context"
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
}
type GMCommandResult struct {
	Character  Character `json:"character"`
	Name       string    `json:"name"`
	Args       []int64   `json:"args"`
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
