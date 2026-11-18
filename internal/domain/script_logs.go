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
	EventCredit int64               `json:"event_credit,omitempty"` // Server-resolved event reward only.
	Quests      []ScriptQuestForce  `json:"quests,omitempty"`
	Travel      *ScriptTravel       `json:"travel,omitempty"`       // Server-resolved destination; never a raw Lua table field.
	PetTraining bool                `json:"pet_training,omitempty"` // Dedicated pet-course NPC only.
	Look        *ScriptLook         `json:"look,omitempty"`         // Beauty host only; never a raw Lua table field.
	Growth      *ScriptGrowth       `json:"growth,omitempty"`       // Generated Legacy NPC sources only.
	// Profession host only; never a raw Lua table field.
	Profession *ProfessionChange `json:"profession,omitempty"`
	// Drops are items left on the character's map for it to pick up (what a
	// gathering node gives). Profession host only.
	Drops []ScriptDrop `json:"drops,omitempty"`
}

// ScriptGrowth is what a generated Legacy NPC source changes on the character
// besides items, mesos, experience and quests: fame (cm.addFame), skill points
// (gainSP / resetSP), the next job of the character's own branch
// (cm.changeJob), taught skills (cm.teachSkill) and a look set without a
// coupon (cm.setHair / setFace / setSkin; the source takes its own payment).
// NPC host only; never a raw Lua table field.
type ScriptGrowth struct {
	Fame   int           `json:"fame,omitempty"`
	SP     int           `json:"sp,omitempty"`
	SPSet  *int          `json:"sp_set,omitempty"`
	Job    int           `json:"job,omitempty"`
	Skills []ScriptSkill `json:"skills,omitempty"`
	Hair   int           `json:"hair,omitempty"`
	Face   int           `json:"face,omitempty"`
	Skin   *int          `json:"skin,omitempty"`
	// Coupon is the 515xxxx cash coupon the source took for the look; cash
	// items cannot leave through the item list, so it is used up here.
	Coupon int `json:"coupon,omitempty"`
}

// ScriptSkill is one cm.teachSkill(id, level, master level).
type ScriptSkill struct {
	ID     int `json:"id"`
	Level  int `json:"level"`
	Master int `json:"master,omitempty"`
}

// ValidScriptGrowth bounds every field; whether the job is one the character
// may take is decided against the stored character.
func ValidScriptGrowth(g ScriptGrowth) bool {
	if g.Fame < -30000 || g.Fame > 30000 || g.SP < -999 || g.SP > 999 || g.SPSet != nil && (*g.SPSet < 0 || *g.SPSet > 999) || len(g.Skills) > 16 {
		return false
	}
	if g.Job != 0 && !ValidJob(g.Job) {
		return false
	}
	if g.Coupon != 0 && (g.Coupon/10000 != 515 || g.Hair == 0 && g.Face == 0 && g.Skin == nil) {
		return false
	}
	for _, s := range g.Skills {
		if s.ID < 1 || s.ID > 99999999 || s.Level < 0 || s.Level > 255 || s.Master < 0 || s.Master > 255 {
			return false
		}
	}
	if g.Hair != 0 && (g.Hair < 30000 || g.Hair > 49999) || g.Face != 0 && (g.Face < 20000 || g.Face > 29999) || g.Skin != nil && (*g.Skin < 0 || *g.Skin > 13) {
		return false
	}
	return true
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
	Pets      []PetChange       `json:"pet_changes,omitempty"`
	Drops     []ItemDrop        `json:"drops,omitempty"`
	// Skills are the skill records a ScriptGrowth taught, as they now stand.
	Skills []SkillState `json:"skills,omitempty"`
}

// ScriptDrop is one stack an exchange leaves on the ground at (X, Y) of the
// character's own map and channel, owned by that character.
type ScriptDrop struct {
	TemplateID    int  `json:"template_id"`
	Quantity      int  `json:"quantity"`
	X             int  `json:"x"`
	Y             int  `json:"y"`
	RollEquipment bool `json:"roll_equipment,omitempty"`
}

type ScriptQuestRepository interface {
	ReadScriptQuestData(context.Context, CharacterSession, int) (ScriptQuestData, error)
}

type ScriptRepository interface {
	ReadScriptLogs(context.Context, CharacterSession, []ScriptLogKey) ([]ScriptLogValue, error)
	ExecuteScriptExchange(context.Context, ScriptExchange) (ScriptExchangeResult, error)
}

// EventItemRepository applies all tracked characters' event items in the same
// transaction. Each exchange has its own durable receipt.
type EventItemRepository interface {
	ExecuteEventItems(context.Context, []ScriptExchange) ([]ScriptExchangeResult, error)
}

// EventLogRepository applies server-created roster logs and reviewed quest resets atomically.
type EventLogRepository interface {
	ExecuteEventLogs(context.Context, []ScriptExchange) ([]ScriptExchangeResult, error)
}
