package protocol

// Field objects (S0b): affected areas (mists, smoke, auras) per
// CAffectedAreaPool::OnAffectedAreaCreated 0x42E886 / Removed 0x42E4C1, and
// town portals (Mystic Door) per CTownPortalPool 0x6EDE72 / 0x6ED585 and
// CWvsContext::OnTownPortal 0x9305A1.
const (
	SendTownPortalInfo      uint16 = 0x56
	SendAffectedAreaCreated uint16 = 0x1e7
	SendAffectedAreaRemoved uint16 = 0x1e8
	SendTownPortalCreated   uint16 = 0x1e9
	SendTownPortalRemoved   uint16 = 0x1ea
	RecvUseTownPortal       uint16 = 0xe7
	NoTownPortal                   = 999999999
)

// Affected area types in the v148 client. Mob areas can damage players,
// ordinary player areas supply skill-specific effects, and smoke grants evasion.
const (
	AreaMob        int32 = 0
	AreaPlayer     int32 = 1
	AreaPoison           = AreaPlayer
	AreaInvincible int32 = 2
	// AreaRecovery is what Java sends for Recovery Aura (isPoisonMist 4); the
	// v145 create path (0x42E8B2) only special-cases type 3.
	AreaRecovery int32 = 4
)

type Rect struct{ Left, Top, Right, Bottom int32 }

type AffectedArea struct {
	ObjectID   int32
	Type       int32
	OwnerID    int64
	SkillID    int
	SkillLevel int
	DelayMS    int // Display delay (ms in a U16).
	Box        Rect
	Element    int32 // Opaque I32 after the rectangle (Java writes 0).
	X, Y       int16
}

// AffectedAreaCreated: oid, type, owner, skill, U8 level, U16 delay, rect
// (4×I32), I32, I16 x, I16 y.
func AffectedAreaCreated(a AffectedArea) []byte {
	if a.ObjectID <= 0 || a.SkillID <= 0 || a.SkillLevel < 0 || a.SkillLevel > 255 || a.Box.Left > a.Box.Right || a.Box.Top > a.Box.Bottom {
		return nil
	}
	w := NewWriter(SendAffectedAreaCreated)
	w.I32(a.ObjectID)
	w.I32(a.Type)
	w.I32(int32(a.OwnerID))
	w.I32(int32(a.SkillID))
	w.U8(byte(a.SkillLevel))
	w.U16(uint16(max(0, min(0xffff, a.DelayMS))))
	w.I32(a.Box.Left)
	w.I32(a.Box.Top)
	w.I32(a.Box.Right)
	w.I32(a.Box.Bottom)
	w.I32(a.Element)
	w.I16(a.X)
	w.I16(a.Y)
	return w.Packet()
}

// AffectedAreaRemoved: oid; Poison Mist (2111003) reads one more byte, the
// eruption flag, which other skills never read.
func AffectedAreaRemoved(objectID int32, skillID int, eruption bool) []byte {
	w := NewWriter(SendAffectedAreaRemoved)
	w.I32(objectID)
	if skillID == 2111003 {
		w.Bool(eruption)
	}
	return w.Packet()
}

// TownPortalCreated: U8 state (0 animated), owner I32, I16 x, I16 y.
func TownPortalCreated(ownerID int64, x, y int16, animated bool) []byte {
	w := NewWriter(SendTownPortalCreated)
	w.Bool(!animated)
	w.I32(int32(ownerID))
	w.I16(x)
	w.I16(y)
	return w.Packet()
}

func TownPortalRemoved(ownerID int64, animated bool) []byte {
	w := NewWriter(SendTownPortalRemoved)
	w.Bool(!animated)
	w.I32(int32(ownerID))
	return w.Packet()
}

// TownPortalInfo tells the owner (and party) where the pair is; town/field
// NoTownPortal clears it.
func TownPortalInfo(townID, fieldID, skillID int, x, y int16) []byte {
	w := NewWriter(SendTownPortalInfo)
	w.I32(int32(townID))
	w.I32(int32(fieldID))
	if townID != NoTownPortal && fieldID != NoTownPortal {
		w.I32(int32(skillID))
		w.I16(x)
		w.I16(y)
	}
	return w.Packet()
}

type UseTownPortal struct {
	OwnerID int64
	ToTown  bool
}

// ReadUseTownPortal follows v148 0x74180C and 0x503EA8: owner I32, U8 mode
// (0 from the field, 1 from the town).
func ReadUseTownPortal(r *Reader) (UseTownPortal, error) {
	owner := int64(r.I32())
	mode := r.U8()
	if r.Err() != nil || r.Remaining() != 0 || owner <= 0 || mode > 1 {
		return UseTownPortal{}, ErrMalformed
	}
	return UseTownPortal{OwnerID: owner, ToTown: mode == 0}, nil
}
