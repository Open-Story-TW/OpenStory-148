package domain

import (
	"context"
	"time"
)

type LogScope string
type LogPeriod string

const (
	LogCharacter LogScope  = "character"
	LogAccount   LogScope  = "account"
	LogWorld     LogScope  = "world"
	LogPermanent LogPeriod = "permanent"
	LogHourly    LogPeriod = "hour"
	LogDaily     LogPeriod = "day"
	LogWeekly    LogPeriod = "week"
	LogMonthly   LogPeriod = "month"
	LogYearly    LogPeriod = "year"
)

// Account logs preserve the legacy account+world scope. Character and account
// identities are always derived from the authenticated session by the store.
type ScriptLogKey struct {
	Scope  LogScope  `json:"scope"`
	Period LogPeriod `json:"period"`
	Name   string    `json:"name"`
}

type ScriptLogValue struct {
	Key         ScriptLogKey `json:"key"`
	WindowStart time.Time    `json:"window_start"`
	Value       int64        `json:"value"`
	Revision    int64        `json:"revision"`
}

type ScriptLogChange struct {
	Key      ScriptLogKey `json:"key"`
	Delta    int64        `json:"delta,omitempty"`
	Set      *int64       `json:"set,omitempty"`
	Expected *int64       `json:"expected,omitempty"`
	Maximum  *int64       `json:"maximum,omitempty"`
}

type ScriptItemDelta struct {
	TemplateID int `json:"template_id"`
	Quantity   int `json:"quantity"`
}

// ScriptQuestData is durable custom quest/dialogue state. It is separate from
// the quest's started/completed status and ordered monster counters.
type ScriptQuestData struct {
	QuestID  int    `json:"quest_id"`
	Data     string `json:"data"`
	Revision int64  `json:"revision"`
}

// ScriptQuestForce is the legacy forceStartQuest/forceCompleteQuest effect: it
// sets the status without checking WZ requirements or granting WZ rewards.
// Only reviewed quest sources may request it.
type ScriptQuestForce struct {
	QuestID int     `json:"quest_id"`
	State   int     `json:"state"` // 1=started, 2=completed.
	Data    *string `json:"data,omitempty"`
}

type ScriptQuestChange struct {
	QuestID  int     `json:"quest_id"`
	Data     string  `json:"data"`
	Expected *string `json:"expected,omitempty"`
}

// ScriptExchange is one durable command, never a transaction held across a
// dialogue yield. Conditional logs and all listed assets commit together.
type ScriptExchange struct {
	Session     CharacterSession    `json:"-"`
	OperationID string              `json:"operation_id"`
	Logs        []ScriptLogChange   `json:"logs,omitempty"`
	MesosDelta  int64               `json:"mesos_delta,omitempty"`
	Items       []ScriptItemDelta   `json:"items,omitempty"`
	QuestData   []ScriptQuestChange `json:"quest_data,omitempty"`
	EXP         int64               `json:"exp,omitempty"`
	Quests      []ScriptQuestForce  `json:"quests,omitempty"`
	Travel      *ScriptTravel       `json:"travel,omitempty"` // Server-resolved destination; never a raw Lua table field.
	Look        *ScriptLook         `json:"look,omitempty"`   // Beauty host only; never a raw Lua table field.
}

// ScriptLook is one beauty salon visit (Legacy cm.setAvatar): one coupon from
// the cash tab is used and the hair or face set in the same transaction.
// Exactly one of Hair and Face is non-zero.
type ScriptLook struct {
	Coupon int `json:"coupon"`
	Hair   int `json:"hair,omitempty"`
	Face   int `json:"face,omitempty"`
}

// ValidScriptLook bounds a look change to a 515xxxx coupon and a hair
// (30000-49999) or face (20000-29999) ID.
func ValidScriptLook(l ScriptLook) bool {
	if l.Coupon/10000 != 515 {
		return false
	}
	if l.Hair != 0 {
		return l.Face == 0 && l.Hair >= 30000 && l.Hair <= 49999
	}
	return l.Face >= 20000 && l.Face <= 29999
}

// ScriptTravel commits a fare/recommendation and the saved destination in the
// same transaction. Coordinates must match a mounted destination portal.
type ScriptTravel struct {
	FromMap    int         `json:"from_map"`
	MapID      int         `json:"map_id"`
	SpawnPoint int         `json:"spawn_point"`
	X          int         `json:"x"`
	Y          int         `json:"y"`
	Taxi       *TaxiChoice `json:"taxi,omitempty"` // Dedicated host only; fresh policy is checked by the store.
}

type ScriptExchangeResult struct {
	InventoryResult
	Logs      []ScriptLogValue  `json:"logs"`
	QuestData []ScriptQuestData `json:"quest_data,omitempty"`
	Quests    []QuestState      `json:"quests,omitempty"`
}

type ScriptQuestRepository interface {
	ReadScriptQuestData(context.Context, CharacterSession, int) (ScriptQuestData, error)
}

type ScriptRepository interface {
	ReadScriptLogs(context.Context, CharacterSession, []ScriptLogKey) ([]ScriptLogValue, error)
	ExecuteScriptExchange(context.Context, ScriptExchange) (ScriptExchangeResult, error)
}
