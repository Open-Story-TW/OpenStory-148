package protocol

import (
	"sort"
	"time"
)

// Monster temporary stats (S0b). v145 CMob uses CFlag<256>: index n is
// 1<<(n%32) in wire dword 7-n/32, built by the a697f3..a69f2c initializers.
// CMob::TempStatDecode 0x6D174C reads, for every ordinary stat, I32 value,
// I32 reason (source skill) and U16 duration in 500ms units, in the fixed
// order below; 27 is a burn list and 29/30/31/37/42 have extra fields.
const (
	SendApplyMonsterStatus  uint16 = 0x1b1
	SendCancelMonsterStatus uint16 = 0x1b2
)

type MobStat uint8

const (
	MobPAD MobStat = iota
	MobPDR
	MobMAD
	MobMDR
	MobACC
	MobEVA
	MobSpeed
	MobStun
	MobFreeze
	MobPoison
	MobSeal
	MobDarkness
	MobPowerUp
	MobMagicUp
	MobPGuardUp
	MobMGuardUp
	MobDoom
	MobWeb
	MobPImmune
	MobMImmune
	MobShowdown
	MobHardSkin
	MobAmbush
	MobDamagedElemAttr
	MobVenom
	MobBlind
	MobSealSkill
	MobBurned
	MobDazzle
	MobPCounter
	MobMCounter
	MobDisable
	MobRiseByToss
	MobBodyPressure
	MobWeakness
	MobTimeBomb
	MobMagicCrash
	MobHealByDamage
	MobStat38
	MobStat39
	MobStat40
	MobStat41
	MobStat42
	MobStat43
	MobStatCount
)

// mobStatDecodeOrder is the TempStatDecode read order for value/reason/time.
var mobStatDecodeOrder = []MobStat{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 18, 19, 16, 17, 21, 22, 24, 25, 26, 28, 29, 30, 32, 33, 34, 35, 20, 36, 23, 38, 39, 40, 41, 42, 43}

// MobStatOrdinary reports whether the stat carries the value/reason/time
// triple. Burned, Disable and HealByDamage need their own encoders.
func MobStatOrdinary(s MobStat) bool {
	return s < MobStatCount && s != MobBurned && s != MobDisable && s != MobHealByDamage
}

// MobStatMoves is sub_6A7948: Speed/Stun/Freeze/Doom/RiseByToss add one
// trailing movement byte to set and reset packets.
func MobStatMoves(s MobStat) bool {
	return s == MobSpeed || s == MobStun || s == MobFreeze || s == MobDoom || s == MobRiseByToss
}

type MobStatValue struct {
	Stat      MobStat
	Value     int32
	Reason    int32 // Source skill ID.
	ExpiresAt time.Time
	Counter   int32 // PCounter/MCounter reflect rate, also the shared counter pair.
}

// MobBurn is one v148 BurnedInfo entry (CMob::TempStatDecode 0x7220C5,
// BurnedInfo::Decode 0x6F1787). The native tick loop uses the remaining tick
// count and the stack count separately.
type MobBurn struct {
	OwnerID        int32
	SkillID        int32
	Damage         int32
	Interval       time.Duration
	ExpiresAt      time.Time
	RemainingTicks int
	Stacks         int
}

func validMobBurn(burn MobBurn) bool {
	return burn.OwnerID > 0 && burn.SkillID > 0 && burn.SkillID <= 0x0fffffff &&
		burn.Stacks > 0 && burn.Stacks <= 15
}

func mobStatMask(w *Writer, stats []MobStat) {
	var dw [8]uint32
	for _, s := range stats {
		dw[7-int(s)/32] |= 1 << (uint(s) % 32)
	}
	for _, v := range dw {
		w.U32(v)
	}
}

func sortedMobStats(values []MobStatValue) ([]MobStatValue, bool) {
	out := append([]MobStatValue(nil), values...)
	seen := map[MobStat]bool{}
	for _, v := range out {
		if !MobStatOrdinary(v.Stat) || seen[v.Stat] {
			return nil, false
		}
		seen[v.Stat] = true
	}
	rank := map[MobStat]int{}
	for i, s := range mobStatDecodeOrder {
		rank[s] = i
	}
	sort.Slice(out, func(i, j int) bool { return rank[out[i].Stat] < rank[out[j].Stat] })
	return out, true
}

// WriteMobTempStat writes the CFlag mask and TempStatDecode values. Used by
// apply-status and by mob enter/control so late joiners see current stats.
func WriteMobTempStat(w *Writer, values []MobStatValue, now time.Time, burns ...MobBurn) {
	sorted, ok := sortedMobStats(values)
	if !ok || len(burns) > 255 {
		w.err = ErrMalformed
		return
	}
	stats := make([]MobStat, 0, len(sorted)+1)
	for _, v := range sorted {
		stats = append(stats, v.Stat)
	}
	if len(burns) > 0 {
		stats = append(stats, MobBurned)
	}
	mobStatMask(w, stats)
	var counter *MobStatValue
	for i, v := range sorted {
		w.I32(v.Value)
		w.I32(v.Reason)
		units := (v.ExpiresAt.Sub(now).Milliseconds() + 499) / 500
		w.U16(uint16(max(0, min(0xffff, units))))
		if v.Stat == MobPCounter || v.Stat == MobMCounter {
			counter = &sorted[i]
		}
	}
	if len(burns) > 0 {
		w.U8(byte(len(burns)))
		for _, b := range burns {
			if !validMobBurn(b) || b.Damage <= 0 || b.Interval < time.Millisecond ||
				b.Interval > 10*time.Minute || b.RemainingTicks < 0 || b.RemainingTicks > 600000 {
				w.err = ErrMalformed
				return
			}
			w.I32(b.OwnerID)
			w.I32(b.SkillID)
			w.I32(b.Damage)
			w.I32(int32(b.Interval.Milliseconds()))
			w.U32(uint32(b.ExpiresAt.UnixMilli()))
			w.I32(int32(b.RemainingTicks))
			w.I32(int32(b.Stacks))
		}
	}
	// Extras after the ordinary triples: PCounter I32, MCounter I32, then the
	// shared counter pair when either is present.
	for _, v := range sorted {
		if v.Stat == MobPCounter {
			w.I32(v.Counter)
		}
	}
	for _, v := range sorted {
		if v.Stat == MobMCounter {
			w.I32(v.Counter)
		}
	}
	if counter != nil {
		w.I32(counter.Counter)
		w.I32(0)
	}
	for _, v := range sorted {
		if v.Stat == MobStat42 {
			w.I32(0)
			w.I32(0)
		}
	}
}

func anyMobStatMoves(values []MobStatValue) bool {
	for _, v := range values {
		if MobStatMoves(v.Stat) {
			return true
		}
	}
	return false
}

// ApplyMonsterStatus follows CMob::OnStatSet 0x5E2F90 / 0x5DEAF3: temp stat,
// U16 delay, U8 calc-damage index, and the movement byte when applicable.
func ApplyMonsterStatus(objectID int32, values []MobStatValue, delay time.Duration, now time.Time, burns ...MobBurn) []byte {
	if objectID <= 0 || len(values)+len(burns) == 0 {
		return nil
	}
	w := NewWriter(SendApplyMonsterStatus)
	w.I32(objectID)
	WriteMobTempStat(w, values, now, burns...)
	w.U16(uint16(max(0, min(0xffff, delay.Milliseconds()))))
	w.U8(1)
	if anyMobStatMoves(values) {
		w.U8(0)
	}
	if w.Err() != nil {
		return nil
	}
	return w.Packet()
}

// CancelMonsterBurns follows the v148 reset branch at 0x70729A. BurnedInfo
// resets identify the owner, skill and number of stacks to remove.
func CancelMonsterBurns(objectID int32, remainingCount int, ended []MobBurn) []byte {
	if objectID <= 0 || remainingCount < 0 || remainingCount > 255 || len(ended) == 0 || len(ended) > 255 {
		return nil
	}
	w := NewWriter(SendCancelMonsterStatus)
	w.I32(objectID)
	mobStatMask(w, []MobStat{MobBurned})
	w.I32(int32(remainingCount))
	w.I32(int32(len(ended)))
	for _, b := range ended {
		if !validMobBurn(b) {
			return nil
		}
		w.I32(b.OwnerID)
		w.U32(uint32(b.SkillID)<<4 | uint32(b.Stacks))
	}
	w.U8(1)
	return w.Packet()
}

// CancelMonsterStatus follows CMob::OnStatReset 0x5E3059 / 0x5E1CC4.
func CancelMonsterStatus(objectID int32, stats []MobStat) []byte {
	if objectID <= 0 || len(stats) == 0 {
		return nil
	}
	values := make([]MobStatValue, 0, len(stats))
	for _, s := range stats {
		values = append(values, MobStatValue{Stat: s})
	}
	if _, ok := sortedMobStats(values); !ok {
		return nil
	}
	w := NewWriter(SendCancelMonsterStatus)
	w.I32(objectID)
	mobStatMask(w, stats)
	w.U8(1)
	if anyMobStatMoves(values) {
		w.U8(0)
	}
	return w.Packet()
}
