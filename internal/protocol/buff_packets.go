package protocol

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"slices"
	"time"
)

// BuffStat is one stat of a temporary-stat packet.
type BuffStat struct {
	Stat  buffstat.Stat
	Value int
}

func buffSpecs(stats []BuffStat) ([]buffstat.Spec, map[int]int, error) {
	if len(stats) == 0 {
		return nil, nil, fmt.Errorf("buff: no stats")
	}
	names := make([]buffstat.Stat, len(stats))
	for i, s := range stats {
		names[i] = s.Stat
	}
	specs, err := buffstat.Resolve(names)
	if err != nil {
		return nil, nil, err
	}
	values := map[int]int{}
	for i, s := range specs {
		values[s.Bit] = stats[i].Value
	}
	return specs, values, nil
}

func writeBuffMask(w *Writer, specs []buffstat.Spec) {
	for _, v := range buffstat.Mask(specs) {
		w.U32(v)
	}
}

// TemporaryStatSet is the local 0x2A packet for one source carrying several
// stats, laid out as encodeForLocal0x6C4EF8 and OnTemporaryStatSet0x939817
// read it: mask, triplets in client order, three unconditional bytes, delay
// U16 and the movement byte when the mask needs it.
func TemporaryStatSet(source int, remaining time.Duration, stats []BuffStat) ([]byte, error) {
	return TemporaryStatSetIndie(source, remaining, stats, nil)
}

// IndieEntry is one source of an indie stat's client list.
type IndieEntry struct {
	Source    int
	Value     int
	Remaining time.Duration
}

// TemporaryStatSetIndie is TemporaryStatSet with the full entry list of some
// indie stats. IndieTempStat::Decode replaces a stat's whole list, so a list
// must name every source that still holds the stat; a stat without a list
// gets the single entry of source.
func TemporaryStatSetIndie(source int, remaining time.Duration, stats []BuffStat, indie map[buffstat.Stat][]IndieEntry) ([]byte, error) {
	specs, values, err := buffSpecs(stats)
	if err != nil {
		return nil, err
	}
	wide := buffstat.IntValues(specs)
	w := NewWriter(0x2d)
	writeBuffMask(w, specs)
	for _, s := range buffstat.LocalTriplets(specs) {
		v := values[s.Bit]
		if wide {
			w.I32(int32(v))
		} else {
			if v < -32768 || v > 32767 {
				return nil, fmt.Errorf("buff: %s value %d exceeds I16", s.Name, v)
			}
			w.I16(int16(v))
		}
		w.I32(int32(source))
		w.I32(int32(max(0, remaining.Milliseconds())))
	}
	w.U8(0)
	w.U8(0)
	w.U8(0)
	// Tail fields are located natively but their meaning is not reviewed;
	// they are sent as zero and each skill's MCP acceptance checks the effect.
	for _, s := range buffstat.LocalTails(specs) {
		for _, v := range s.Tail {
			switch v {
			case buffstat.ValueU8:
				w.U8(0)
			case buffstat.ValueI16:
				w.I16(0)
			case buffstat.ValueI32:
				w.I32(0)
			case buffstat.ValueI32x22:
				w.Zero(22 * 4)
			}
		}
	}
	writeTwoStates(w, specs, values, source, remaining)
	key := int32(time.Now().UnixMilli())
	for _, s := range buffstat.Indies(specs) {
		// Each entry: source, value, key (a new key restarts the client's
		// timer), duration.
		entries, ok := indie[s.Name]
		if !ok {
			entries = []IndieEntry{{Source: source, Value: values[s.Bit], Remaining: remaining}}
		}
		w.I32(int32(len(entries)))
		for _, e := range entries {
			w.I32(int32(e.Source))
			w.I32(int32(e.Value))
			w.I32(key)
			w.I32(int32(max(0, e.Remaining.Milliseconds())))
		}
	}
	w.U16(0)
	// v148 CWvsContext::OnTemporaryStatSet (0x9D3128) reads one more byte
	// after the u16, before the movement byte.
	w.U8(0)
	if buffstat.HasMoveTail(specs) {
		w.U8(0)
	}
	return w.Packet(), w.err
}

// writeTwoStates encodes the TwoStateTemporaryStat elements (bits 248..255):
// value, reason, last-updated "now", then the class-specific fields. Homing
// Beacon's two extra I32 carry no reviewed meaning yet and are sent as zero.
func writeTwoStates(w *Writer, specs []buffstat.Spec, values map[int]int, source int, remaining time.Duration) {
	// The term is whole seconds rounded up: a 1 s buff (Tornado Spin) is
	// published a few ms after its commit and must not become term 0.
	term := uint16(min(65535, max(0, int((remaining+time.Second-1)/time.Second))))
	for _, s := range buffstat.TwoStates(specs) {
		w.I32(int32(values[s.Bit]))
		w.I32(int32(source))
		w.U8(0)
		w.I32(0)
		switch s.TwoState {
		case buffstat.TwoStateTerm:
			w.U16(term)
		case buffstat.TwoStateBooster:
			w.U8(0)
			w.I32(0)
			w.U16(term)
		case buffstat.TwoStateGuided:
			w.I32(0)
			w.I32(0)
		}
	}
}

// ForeignTemporaryStatSet is the remote 0x12E packet: mask (every stat, even
// the ones other clients ignore), payloads in encodeForRemote0x6AFDAB order,
// three unconditional bytes and the delay U16 read by 0x8B3F35.
func ForeignTemporaryStatSet(characterID int64, source int, stats []BuffStat) ([]byte, error) {
	return ForeignTemporaryStatSetFor(characterID, source, 0, stats)
}

// ForeignTemporaryStatSetFor is ForeignTemporaryStatSet with the remaining
// time two-state terms carry.
func ForeignTemporaryStatSetFor(characterID int64, source int, remaining time.Duration, stats []BuffStat) ([]byte, error) {
	specs, values, err := buffSpecs(stats)
	if err != nil {
		return nil, err
	}
	w := NewWriter(0x145)
	w.I32(int32(characterID))
	writeBuffMask(w, specs)
	for _, s := range buffstat.RemoteSorted(specs) {
		v := values[s.Bit]
		switch {
		case len(s.Remote) == 0:
		case slices.Equal(s.Remote, []buffstat.Value{buffstat.ValueU8}):
			w.U8(uint8(v))
		case slices.Equal(s.Remote, []buffstat.Value{buffstat.ValueI16}):
			w.I16(int16(v))
		case slices.Equal(s.Remote, []buffstat.Value{buffstat.ValueI32}):
			w.I32(int32(v))
		case slices.Equal(s.Remote, []buffstat.Value{buffstat.ValueI16, buffstat.ValueI32}):
			w.I16(int16(v))
			w.I32(int32(source))
		default:
			return nil, fmt.Errorf("buff: stat %s needs a reviewed remote encoding", s.Name)
		}
	}
	w.U8(0)
	w.U8(0)
	w.U8(0)
	writeTwoStates(w, specs, values, source, remaining)
	w.U16(0)
	return w.Packet(), w.err
}

// TemporaryStatReset cancels stats locally (characterID 0, 0x2B) or for
// another user (0x12F).
func TemporaryStatReset(characterID int64, stats []buffstat.Stat) ([]byte, error) {
	if slices.Contains(stats, "MECH_CHANGE") && !slices.Contains(stats, "BIT_137") {
		// Ending a Mechanic mode also resets bit 137, which (S2's client
		// reading) is the only reset that clears the mech cast gate; resetting
		// an unset stat is a no-op on the client.
		stats = append(slices.Clone(stats), "BIT_137")
	}
	values := make([]BuffStat, len(stats))
	for i, s := range stats {
		values[i] = BuffStat{Stat: s}
	}
	specs, _, err := buffSpecs(values)
	if err != nil {
		return nil, err
	}
	op := uint16(0x2e)
	if characterID != 0 {
		op = 0x146
	}
	w := NewWriter(op)
	if characterID != 0 {
		w.I32(int32(characterID))
	}
	writeBuffMask(w, specs)
	if characterID == 0 {
		// Local reset sub_9319A3 runs IndieTempStat::Decode 0x6C4A45 for each
		// indie stat in the mask (sub_6C4EAB): the stat's remaining entry
		// list, here empty. Without it the client reads past the packet and
		// disconnects (error 38 when ANGEL_ATK expired). The remote reset
		// 0x8B38CA reads no indie data.
		for range buffstat.Indies(specs) {
			w.I32(0)
		}
	}
	// Then the movement byte (MoveTail stats) and the riding flag byte; both
	// are ignored when the mask has neither.
	w.U8(3)
	w.U8(1)
	return w.Packet(), w.err
}
