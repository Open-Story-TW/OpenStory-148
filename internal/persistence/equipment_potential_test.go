package persistence

import (
	"encoding/json"
	"testing"

	"maple-go-server/internal/domain"
)

func TestEquipmentPotentialMetadataJSONPreservesOpaqueAndPresence(t *testing.T) {
	for _, p := range []domain.EquipmentPotential{
		{Present: true},
		{Present: true, RawState: 1, Options: [5]uint16{0xffef, 0xffec, 0, 65535, 123}, Flags: 65535},
		{Present: true, RawState: 255, Options: [5]uint16{1, 2, 3, 4, 5}},
	} {
		before := equipmentMetadata{Potential: p, Enhance: 255}
		raw, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		var after equipmentMetadata
		if err = json.Unmarshal(raw, &after); err != nil || after != before {
			t.Fatalf("metadata roundtrip before=%+v after=%+v err=%v", before, after, err)
		}
	}
	var old equipmentMetadata
	if err := json.Unmarshal([]byte(`{"scroll_level":1,"flags":2}`), &old); err != nil || !old.Potential.IsZero() || old.Enhance != 0 {
		t.Fatalf("legacy metadata=%+v %v", old, err)
	}
}

func TestEquipmentMetadataRejectsUnknownFieldsWithoutPartialMutation(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"scroll_level":1,"future_enhance":7}`,
		`{"flags":2,"potential":{"raw_state":1,"future_option":9}}`,
		`{"scroll_level":1} {"flags":2}`,
	} {
		before := equipmentMetadata{ScrollLevel: 3, Potential: domain.EquipmentPotential{Present: true, RawState: 255}}
		after := before
		if err := after.UnmarshalJSON([]byte(raw)); err == nil || after != before {
			t.Fatalf("unknown/lossy metadata accepted or partially applied: raw=%s after=%+v err=%v", raw, after, err)
		}
	}
}
