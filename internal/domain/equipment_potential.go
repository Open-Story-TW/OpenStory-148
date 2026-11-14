package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// EquipmentPotential preserves the native equipment tail as detached values.
// RawState is not Java's signed potential-ID representation. Options and Flags
// remain opaque until an authoritative option policy interprets them.
type EquipmentPotential struct {
	// Present distinguishes an explicitly observed all-zero native tail from
	// legacy equipment whose original sentinel tail must remain unchanged.
	// It is persistence metadata, never an extra native wire field.
	Present  bool      `json:"present"`
	RawState uint8     `json:"raw_state,omitempty"`
	Options  [5]uint16 `json:"options"`
	Flags    uint16    `json:"flags,omitempty"`
}

// IsZero preserves the JSON shape of legacy equipment without potential data.
func (p EquipmentPotential) IsZero() bool { return p == (EquipmentPotential{}) }

// UnmarshalJSON rejects option arrays that encoding/json would otherwise
// silently truncate or zero-pad. Every U8/U16 bit pattern remains valid here;
// this layer preserves data rather than interpreting option/rank policies.
func (p *EquipmentPotential) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("equipment potential must be an object")
	}
	var wire struct {
		Present  *bool           `json:"present"`
		RawState uint8           `json:"raw_state"`
		Options  json.RawMessage `json:"options"`
		Flags    uint16          `json:"flags"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("equipment potential requires one JSON object")
	}
	// Old JSON without an explicit presence marker denotes observed data.
	// New standalone/audit values preserve an explicit false marker too, so
	// encoding an absent Before value never changes a frozen receipt/hash.
	next := EquipmentPotential{Present: true, RawState: wire.RawState, Flags: wire.Flags}
	if wire.Present != nil {
		next.Present = *wire.Present
	}
	if len(wire.Options) != 0 {
		var options []uint16
		if err := json.Unmarshal(wire.Options, &options); err != nil {
			return err
		}
		if len(options) != len(next.Options) {
			return fmt.Errorf("equipment potential requires exactly five options")
		}
		copy(next.Options[:], options)
	}
	*p = next
	return nil
}
