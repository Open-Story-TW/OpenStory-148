// Packet layouts derived from the Legacy/OdinMS Java source, licensed AGPLv3.
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import (
	"math"
	"net"
	"sort"
	"time"

	"maple-go-server/internal/domain"
)

const fileTimeOffset int64 = 116445024000000000 // Legacy PacketHelper.FT_UT_OFFSET.
const maxTime int64 = 150842304000000000
const zeroTime int64 = 94354848000000000

func mapleTime(t time.Time) int64     { return t.UnixMilli()*10000 + fileTimeOffset }
func reversedLong(w *Writer, v int64) { w.U32(uint32(uint64(v) >> 32)); w.U32(uint32(v)) }
func Simple(op uint16) []byte         { return NewWriter(op).Packet() }
func ShowMapleStory() []byte          { return Simple(SendShowMapleStory) }
func LoginBackground() []byte {
	w := NewWriter(SendLoginBackground)
	w.String("MapLogin2")
	now := time.Now().In(time.FixedZone("TMS", 8*3600))
	w.I32(int32(now.Year()*10000 + int(now.Month())*100 + now.Day()))
	w.U8(1)
	return w.Packet()
}
func LoginFailure(reason byte) []byte {
	w := NewWriter(SendLoginStatus)
	w.U8(reason)
	w.U8(0)
	w.I32(0)
	return w.Packet()
}
func LoginSuccess(a domain.Account) []byte {
	if a.ID <= 0 || a.ID > math.MaxInt32 {
		return nil
	}
	w := NewWriter(SendLoginStatus)
	w.U8(0)
	w.I32(int32(a.ID))
	w.U8(byte(a.Gender))
	w.U8(0)
	w.U8(0)
	w.I32(0)
	w.String(a.Name)
	w.I32(1)
	w.Zero(2) // v148 CLogin::OnCheckPasswordResult reads two bytes here (v145 read three).
	w.I64(mapleTime(time.Now()))
	w.U8(0)
	w.I64(100)
	w.U8(1)
	return w.Packet()
}
func ChooseGender(name string) []byte {
	w := NewWriter(SendChooseGender)
	w.String(name)
	return w.Packet()
}
func GenderSet(name string) []byte { w := NewWriter(SendGenderSet); w.String(name); return w.Packet() }
func Recommended(world int) []byte {
	w := NewWriter(SendRecommended)
	w.I32(int32(world))
	return w.Packet()
}
func WorldList(world int, name string, channels int) []byte {
	if world < 0 || world > 254 || channels < 1 || channels > 20 {
		return nil
	}
	w := NewWriter(SendWorldList)
	w.U8(byte(world))
	w.U8(0)
	w.String(name)
	w.U8(3)
	w.String("Go development world")
	w.U16(100)
	w.U16(100)
	w.U8(byte(channels))
	for ch := 0; ch < channels; ch++ {
		w.String(name + "-" + itoa(ch+1))
		w.I32(100)
		w.U8(byte(world))
		w.U16(uint16(ch))
	}
	w.U16(0)
	w.I32(0)
	return w.Packet()
}
func itoa(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return string(rune('0'+v/10)) + string(rune('0'+v%10))
}

// v148 reads one more byte after the end marker; non-zero shows notice 67.
func EndWorldList() []byte {
	w := NewWriter(SendWorldList)
	w.U8(255)
	w.U8(255)
	w.U8(0)
	return w.Packet()
}
func ServerStatus(status uint16) []byte {
	w := NewWriter(SendServerStatus)
	w.U16(status)
	return w.Packet()
}
func NameResult(name string, taken bool) []byte {
	w := NewWriter(SendNameResult)
	w.String(name)
	w.Bool(taken)
	return w.Packet()
}
func CharacterList(chars []domain.CharacterSummary, slots int) []byte {
	return characterListAt(chars, slots, time.Now())
}
func characterListAt(chars []domain.CharacterSummary, slots int, now time.Time) []byte {
	if len(chars) > domain.MaxCharacterSlots || slots < 1 || slots > domain.MaxCharacterSlots {
		return nil
	}
	w := NewWriter(SendCharacterList)
	w.U8(0)
	// v148 CLogin::OnSelectWorldResult (0x5B4DA1) no longer reads the i32
	// that v145 read before the count.
	w.U8(byte(len(chars)))
	for _, c := range chars {
		if !validCharacter(c) {
			return nil
		}
		addCharStats(w, c, now)
		addCharLook(w, c)
		w.U8(0)
		w.U8(0)
	}
	w.U8(3)
	w.U8(0)
	w.I32(int32(slots))
	w.I32(0)
	// v148 adds a high/low i32 pair (minus the client clock) before the
	// i32 that v145 read here.
	reversedLong(w, mapleTime(now))
	w.I32(-1)
	reversedLong(w, mapleTime(now))
	return w.Packet()
}
func CreateCharacterResult(c domain.CharacterSummary, worked bool) []byte {
	w := NewWriter(SendCreateResult)
	w.Bool(!worked)
	// A rejected creation has no character entry to decode.
	if !worked {
		return w.Packet()
	}
	if !validCharacter(c) {
		return nil
	}
	addCharStats(w, c, time.Now())
	addCharLook(w, c)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
func ServerIP(host string, port int, clientID int64) []byte {
	ip := net.ParseIP(host).To4()
	if ip == nil || port < 1 || port > 65535 || clientID < 1 || clientID > math.MaxInt32 {
		return nil
	}
	w := NewWriter(SendServerIP)
	w.U16(0)
	w.Bytes(ip)
	w.U16(uint16(port))
	w.I32(int32(clientID))
	w.Zero(5)
	return w.Packet()
}
func ChannelChange(host string, port int) []byte {
	ip := net.ParseIP(host).To4()
	if ip == nil || port < 1 || port > 65535 {
		return nil
	}
	w := NewWriter(SendChannelChange)
	w.U8(1)
	w.Bytes(ip)
	w.U16(uint16(port))
	w.U8(0)
	return w.Packet()
}
func validCharacter(c domain.Character) bool {
	// Every v145 job renders natively; 130 keeps its reviewed level floor.
	jobSupported := domain.ValidJob(c.Job) && c.SubJob >= 0 && c.SubJob <= 2 && (c.Job != 130 || c.Level >= 30)
	return c.ID > 0 && c.ID <= math.MaxInt32 && jobSupported && c.EXP >= 0 && c.EXP <= domain.MaxExperience() && c.Mesos >= 0 && c.Mesos <= domain.MaxMoney()
}

// addCharStats preserves the Legacy level32 and TMS tail, including swapped
// FILETIME words. writeSP picks the client's single or per-book SP layout.
func addCharStats(w *Writer, c domain.Character, now time.Time) {
	w.I32(int32(c.ID))
	w.FixedString(c.Name, 15)
	w.U8(byte(c.Gender))
	w.U8(byte(c.Skin))
	w.I32(int32(c.Face))
	w.I32(int32(c.Hair))
	writeLevel(w, c.Level)
	w.U16(uint16(c.Job))
	writeStat(w, c.STR)
	writeStat(w, c.DEX)
	writeStat(w, c.INT)
	writeStat(w, c.LUK)
	w.I32(int32(c.HP))
	w.I32(int32(c.WireMaxHP()))
	w.I32(int32(c.MP))
	w.I32(int32(c.WireMaxMP()))
	writeStat(w, c.AP)
	writeSP(w, c)
	writeEXP(w, c.EXP)
	w.I32(int32(c.Fame))
	w.I32(0)
	w.I64(mapleTime(now))
	w.I32(int32(c.MapID))
	w.U8(byte(c.SpawnPoint))
	w.U16(uint16(c.SubJob))
	if domain.IsDemonJob(c.Job) {
		w.I32(int32(c.DemonMark))
	}
	w.U8(0)
	sg := now.In(time.FixedZone("TMS", 8*3600))
	date := sg.Year()*1000000 + int(sg.Month())*10000 + sg.Day()*100 + sg.Hour()
	w.I32(int32(date))
	w.Zero(6 * 4)
	w.Zero(21) // v148 widened this block from 12 to 21 bytes.
	w.I32(0)
	w.U8(0)
	w.I32(0)
	w.U8(6)
	w.I32(0)
	// v148 GW_CharacterStat::Decode (0x4D122D) adds u8, three i32, u8 and a
	// six-entry {i32, u8, i32} table before the reversed time.
	w.U8(0)
	w.I32(0)
	w.I32(0)
	w.I32(0)
	w.U8(0)
	w.Zero(6 * 9)
	reversedLong(w, mapleTime(now))
	w.Zero(25)
	w.Bytes([]byte{1, 1, 1, 1, 1})
}
func addCharLook(w *Writer, c domain.Character) {
	addCharLookMode(w, c, true)
}

func addCharLookMode(w *Writer, c domain.Character, mega bool) {
	w.U8(byte(c.Gender))
	w.U8(byte(c.Skin))
	w.I32(int32(c.Face))
	w.I32(int32(c.Job))
	w.Bool(!mega)
	w.I32(int32(c.Hair))
	equips := append([]domain.Equipment(nil), c.Equipment...)
	sort.Slice(equips, func(i, j int) bool { return equips[i].Slot > equips[j].Slot })
	visible := make([]domain.Equipment, 0, len(equips))
	positions := make(map[int]int)
	cashWeapon := 0
	for _, e := range equips {
		pos := -e.Slot
		if pos > 0 && pos < 100 {
			positions[pos] = len(visible)
			visible = append(visible, e)
		} else if pos > 100 && pos <= 127 {
			if pos == 111 {
				cashWeapon = e.ItemID
				continue
			}
			pos -= 100
			e.Slot = -pos
			if index, ok := positions[pos]; ok {
				visible[index] = e
			} else {
				positions[pos] = len(visible)
				visible = append(visible, e)
			}
		}
	}
	for _, e := range visible {
		w.U8(byte(-e.Slot))
		w.I32(int32(e.ItemID))
	}
	w.U8(255)
	w.U8(255)
	w.U8(255)
	w.I32(int32(cashWeapon))
	w.U8(0)
	w.Zero(12)
	if domain.IsDemonJob(c.Job) {
		w.I32(int32(c.DemonMark))
	}
}

// v148 CP_CheckPassword (0x15, client sub_5B5AE7) moved the credentials to
// the tail: 16-byte machine id, u32 session key, u8 app mode, u8 0, u8 login
// mode, then id and password. Login mode 0 is the native ID/password form;
// a non-zero mode carries two client-generated token strings in the same
// slots, which this server treats as credentials as well.
func ReadLoginRequest(r *Reader) (name, password string) {
	r.Skip(16)
	r.U32()
	r.U8()
	r.U8()
	r.U8()
	return r.String(), r.String()
}

// LoginRequest writes the v148 login body for test and smoke clients.
func LoginRequest(name, password string) []byte {
	w := NewWriter(RecvLogin)
	w.Bytes(make([]byte, 16))
	w.U32(0)
	w.U8(2)
	w.U8(0)
	w.U8(0)
	w.String(name)
	w.String(password)
	return w.Packet()
}
