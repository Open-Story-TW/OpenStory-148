package protocol

const (
	RecvBattleSquareEnter      uint16 = 0x4a // CUIPvPLauncher 0x810B28, 0x810C2E
	RecvBattleSquarePartyEnter uint16 = 0x4b // CUIPvPLauncher 0x810B8E, 0x810C2E
	RecvBattleSquareList       uint16 = 0x4f // CUIPvPLauncher 0x813DC2, no body
	RecvBattleSquareLeave      uint16 = 0x50 // FieldType 36 menu to 0x9ADD7B
	RecvBattleSquareAttack     uint16 = 0x5b // FieldType 36 check and writer 0x92CCD0
	SendBattleSquareList       uint16 = 0xf5 // CField 0x51F2A5, CUIPvPLauncher 0x80BF89
	SendBattleSquareBlocked    uint16 = 0xda // CField 0x51F145, sub_503ABC
)

const battleSquareUI = 50

// OpenBattleSquare sends the native CUIPvPLauncher open action. The launcher
// requests its availability on 0x4f after it has been created.
func OpenBattleSquare() []byte {
	w := NewWriter(SendOpenUI)
	w.I32(battleSquareUI)
	return w.Packet()
}

// BattleSquareList matches the availability decoder 0x80BF89. Each record
// has one signed slot count and a fixed 100-byte occupancy buffer (0x4C9FE2).
// The buffer is empty here because this implementation owns one arena per
// channel and has no sub-room occupancy to send.
func BattleSquareList(playerCount int) []byte {
	w := NewWriter(SendBattleSquareList)
	w.U8(1)
	w.I32(int32(playerCount))
	w.Zero(100)
	w.U16(100) // ordinary reward multiplier
	w.U8(0)
	return w.Packet()
}

// BattleSquareEntry is the fixed-width request from CUIPvPLauncher. The
// first byte after Tick selects an entry in the received room list.
type BattleSquareEntry struct {
	Tick       uint32
	Index      uint8
	Mode, Rank uint8
	Option     uint8
	Reserved   [2]uint32
}

func ReadBattleSquareEntry(r *Reader) (BattleSquareEntry, error) {
	q := BattleSquareEntry{Tick: r.U32(), Index: r.U8(), Mode: r.U8(), Rank: r.U8(), Option: r.U8()}
	q.Reserved = [2]uint32{r.U32(), r.U32()}
	if r.Err() != nil || r.Remaining() != 0 || q.Mode > 3 || q.Rank > 3 {
		return BattleSquareEntry{}, ErrMalformed
	}
	return q, nil
}

func BattleSquareBlocked(reason byte) []byte {
	w := NewWriter(SendBattleSquareBlocked)
	w.U8(reason)
	return w.Packet()
}

const (
	SendBattleSquareState   uint16 = 0x20d // CField_PvP 0x53CDD2
	SendBattleSquareEnabled uint16 = 0x210 // CField_PvP 0x53D019
	SendBattleSquareScore   uint16 = 0x211 // CField_PvP 0x53FE7D
)

// BattleSquareSurvivalState is the free-for-all form. Modes 1-3 add a team
// roster, as required by a separate branch of the native decoder.
func BattleSquareSurvivalState(rank uint8, active bool) []byte {
	w := NewWriter(SendBattleSquareState)
	w.U8(0)
	w.U8(rank)
	if active {
		w.U8(1)
	} else {
		w.U8(2)
	}
	return w.Packet()
}

func BattleSquareEnable() []byte {
	w := NewWriter(SendBattleSquareEnabled)
	w.U8(1)
	return w.Packet()
}

func BattleSquareScore(score int) []byte {
	w := NewWriter(SendBattleSquareScore)
	w.I32(int32(score))
	w.U8(0)
	return w.Packet()
}

// BattleSquareAttack is the proven fixed part of a native PvP attack. The
// trailing coordinates are used by selected skills; a basic swing has none.
type BattleSquareAttack struct {
	SkillID    uint32
	Level      uint8
	Charge     uint32
	FacingLeft bool
	Action     uint32
	Speed      uint8
	Selector   uint16
	X, Y       int16
	TargetData uint32
	ActionFlag uint8
}

func ReadBattleSquareBasicAttack(r *Reader) (BattleSquareAttack, error) {
	q := BattleSquareAttack{SkillID: r.U32(), Level: r.U8(), Charge: r.U32()}
	facing := r.U8()
	q.FacingLeft = facing == 1
	q.Action, q.Speed, q.Selector = r.U32(), r.U8(), r.U16()
	q.X, q.Y, q.TargetData, q.ActionFlag = r.I16(), r.I16(), r.U32(), r.U8()
	if r.Err() != nil || r.Remaining() != 0 || q.SkillID != 0 || q.Level != 0 || facing > 1 {
		return BattleSquareAttack{}, ErrMalformed
	}
	return q, nil
}
