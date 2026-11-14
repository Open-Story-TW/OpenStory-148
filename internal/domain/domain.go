package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrBadCredentials  = errors.New("invalid credentials")
	ErrAlreadyOnline   = errors.New("account already online")
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidInput    = errors.New("invalid input")
	ErrStaleOwner      = errors.New("stale owner or revision")
	ErrTransferInvalid = errors.New("invalid or expired transfer")
	ErrLeaseBusy       = errors.New("world already has an active owner")
	ErrInsufficient    = errors.New("insufficient funds")
)

type Account struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Gender        int    `json:"gender"` // 255 means account setup has not completed.
	PINConfigured bool   `json:"pin_configured"`
}

type Equipment struct {
	Potential    EquipmentPotential `json:"potential,omitzero"`
	Enhance      uint8              `json:"enhance,omitempty"`
	CashID       int64              `json:"cash_id,omitempty"`
	ExpiresAt    int64              `json:"expires_at,omitempty"`
	ScrollLevel  int                `json:"scroll_level,omitempty"`
	Flags        int                `json:"flags,omitempty"`
	ID           int64              `json:"id"`
	ItemID       int                `json:"item_id"`
	Slot         int                `json:"slot"`
	STR          int                `json:"str"`
	DEX          int                `json:"dex"`
	INT          int                `json:"int"`
	LUK          int                `json:"luk"`
	HP           int                `json:"hp"`
	MP           int                `json:"mp"`
	WATK         int                `json:"watk"`
	MATK         int                `json:"matk"`
	WDEF         int                `json:"wdef"`
	MDEF         int                `json:"mdef"`
	ACC          int                `json:"acc"`
	AVOID        int                `json:"avoid"`
	Speed        int                `json:"speed"`
	Jump         int                `json:"jump"`
	UpgradeSlots int                `json:"upgrade_slots"`
}

// InventoryItem is a stackable bag slot (USE/SETUP/ETC/CASH). Not equipment.
type InventoryItem struct {
	CashID     int64 `json:"cash_id,omitempty"`
	ExpiresAt  int64 `json:"expires_at,omitempty"`
	ID         int64 `json:"id"`
	TemplateID int   `json:"template_id"`
	InvType    int   `json:"inv_type"`
	Slot       int   `json:"slot"`
	Quantity   int   `json:"quantity"`
	Revision   int64 `json:"revision"`
}

// Character is a detached value object. It never holds SQL/Ent handles or locks.
type Character struct {
	ID             int64           `json:"id"`
	AccountID      int64           `json:"account_id"`
	WorldID        int             `json:"world_id"`
	Name           string          `json:"name"`
	Gender         int             `json:"gender"`
	Skin           int             `json:"skin"`
	Face           int             `json:"face"`
	Hair           int             `json:"hair"`
	Level          int             `json:"level"`
	Job            int             `json:"job"`
	SubJob         int             `json:"sub_job,omitempty"`
	DemonMark      int             `json:"demon_mark,omitempty"`
	STR            int             `json:"str"`
	DEX            int             `json:"dex"`
	INT            int             `json:"int"`
	LUK            int             `json:"luk"`
	HP             int             `json:"hp"`
	MP             int             `json:"mp"`
	MaxHP          int             `json:"max_hp"`
	MaxMP          int             `json:"max_mp"`
	BaseMaxHP      int             `json:"base_max_hp"`
	BaseMaxMP      int             `json:"base_max_mp"`
	VitalsModel    int             `json:"vitals_model"`
	VitalsOrigin   string          `json:"vitals_origin,omitempty"`
	AP             int             `json:"ap"`
	SP             int             `json:"sp"`
	SPBooks        SPBookTable     `json:"sp_books,omitzero"`
	EXP            int64           `json:"exp"`
	Fame           int             `json:"fame"`
	Mesos          int64           `json:"mesos"`
	MapID          int             `json:"map_id"`
	SpawnPoint     int             `json:"spawn_point"`
	X              int             `json:"x"`
	Y              int             `json:"y"`
	GM             int             `json:"gm"`
	Equipment      []Equipment     `json:"equipment"`
	Items          []InventoryItem `json:"items"`
	InventorySlots [5]int          `json:"inventory_slots,omitzero"`
	Quests         []QuestState    `json:"quests,omitempty"`
	// QuestRecords is the custom data string per quest ID (Legacy info quests).
	QuestRecords   map[int]string  `json:"quest_records,omitempty"`
	Skills         []SkillState    `json:"skills,omitempty"`
	SkillCooldowns []SkillCooldown `json:"skill_cooldowns,omitempty"`
	SkillBuffs     []SkillBuff     `json:"skill_buffs,omitempty"`
	// Professions, the recipes learned from recipe items, the six personality
	// traits and the gathering/crafting fatigue (already worn off to now).
	Professions []ProfessionState `json:"professions,omitempty"`
	Recipes     []int             `json:"recipes,omitempty"`
	Traits      Traits            `json:"traits,omitzero"`
	Fatigue     int               `json:"fatigue,omitempty"`
	Pets        []Pet             `json:"pets,omitempty"`
	Guild       GuildTag          `json:"guild,omitzero"`
	Revision    int64             `json:"revision"`
}

// Summary is read only from the dedicated roster projection, never by loading
// character aggregates in a loop. The first milestone has no quest/skill payload.
type CharacterSummary = Character

type CreateCharacterRequest struct {
	LoginSession LoginSession `json:"login_session"`
	Lease        WorldLease   `json:"lease"`
	AccountID    int64        `json:"account_id"`
	WorldID      int          `json:"world_id"`
	Name         string       `json:"name"`
	Family       int          `json:"family"` // MakeCharInfo family; zero is Explorer
	SubJob       int          `json:"sub_job"`
	Gender       int          `json:"gender"`
	Skin         int          `json:"skin"`
	Face         int          `json:"face"`
	Hair         int          `json:"hair"`
	Top          int          `json:"top"`
	Bottom       int          `json:"bottom"`
	Shoes        int          `json:"shoes"`
	Weapon       int          `json:"weapon"`
	OperationID  string       `json:"operation_id"`
}

type LoginSession struct {
	AccountID  int64  `json:"account_id"`
	SessionID  string `json:"session_id"`
	Generation int64  `json:"generation"`
}

type WorldLease struct {
	WorldID    int       `json:"world_id"`
	InstanceID string    `json:"instance_id"`
	Epoch      int64     `json:"epoch"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type TransferRequest struct {
	AccountID   int64  `json:"account_id"`
	CharacterID int64  `json:"character_id"`
	WorldID     int    `json:"world_id"`
	ChannelID   int    `json:"channel_id"`
	SessionID   string `json:"session_id"`
	ClientIP    string `json:"client_ip"`
}

type Transfer struct {
	ID           string    `json:"id"`
	Token        string    `json:"token"`
	CharacterID  int64     `json:"character_id"`
	ClientHandle int64     `json:"client_handle"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type TransferClaim struct {
	CharacterID  int64
	ClientHandle int64
	WorldID      int
	ChannelID    int
	Token        string
	ClientIP     string
	Lease        WorldLease
	// Explicit development-only compatibility; both endpoints must be loopback.
	// Production must require Token or a verified client-side equivalent.
	AllowLegacyLoopback bool
}

type MesoAdjustment struct {
	Session     CharacterSession
	Delta       int64
	OperationID string
	Reason      string
}

// UseItemRequest identifies one native USE_ITEM request. The client tick is
// scoped to the authenticated session/generation, not a client-selected asset ID.
type UseItemRequest struct {
	Session    CharacterSession
	ClientTick uint32
	Slot       int
	TemplateID int
}

type CharacterSession struct {
	AccountID   int64      `json:"account_id"`
	CharacterID int64      `json:"character_id"`
	SessionID   string     `json:"session_id"`
	Generation  int64      `json:"generation"`
	ChannelID   int        `json:"channel_id"`
	Lease       WorldLease `json:"lease"`
	Character   Character  `json:"character"`
}

type Repository interface {
	Ping(context.Context) error
	Register(context.Context, string, string) (Account, error)
	Authenticate(context.Context, string, string) (Account, error)
	ConfigureAccount(context.Context, int64, int, string) error
	VerifyPIN(context.Context, int64, string) error
	BeginLogin(context.Context, int64, string) (LoginSession, error)
	TouchLogin(context.Context, LoginSession) error
	EndLogin(context.Context, LoginSession) error
	ListCharacters(context.Context, int64, int) ([]CharacterSummary, error)
	CharacterNameAvailable(context.Context, string) (bool, error)
	CreateCharacter(context.Context, CreateCharacterRequest) (Character, error)
	GetCharacter(context.Context, int64) (Character, error)
	CreateTransfer(context.Context, TransferRequest) (Transfer, error)
	ConsumeTransfer(context.Context, TransferClaim) (CharacterSession, error)
	BeginChannelTransfer(context.Context, CharacterSession, int) error
	FinishChannelTransfer(context.Context, CharacterSession, int) (CharacterSession, error)
	SaveCharacter(context.Context, CharacterSession, Character) error
	AdjustMesos(context.Context, MesoAdjustment) (Character, error)
	UseItem(context.Context, UseItemRequest) (Character, error)
	ReleaseCharacter(context.Context, CharacterSession) error
	AcquireWorldLease(context.Context, int, string) (WorldLease, error)
	RenewWorldLease(context.Context, WorldLease) (WorldLease, error)
	ReleaseWorldLease(context.Context, WorldLease) error
}
