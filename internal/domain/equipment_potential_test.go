package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestEquipmentPotentialValueAndPortableJSON(t *testing.T) {
	e := Equipment{ID: 73, ItemID: 1322005, Slot: -11, Enhance: 255,
		Potential: EquipmentPotential{Present: true, RawState: 255, Options: [5]uint16{0xffef, 0xffec, 65535, 0, 17}, Flags: 0xffff}}
	copy := e
	copy.Potential.Options[0] = 1
	if e.Potential.Options[0] != 0xffef {
		t.Fatal("equipment copies share mutable potential")
	}
	for name, value := range map[string]any{
		"equipment": e,
		"drop":      ItemDrop{Equipment: &e},
		"change":    InventoryChange{Equipment: &e},
		"receipt":   InventoryResult{Character: Character{Equipment: []Equipment{e}}, Changes: []InventoryChange{{Equipment: &e}}},
		"roster":    RosterView(Character{Equipment: []Equipment{e}}),
		"trade":     TradeResult{First: InventoryResult{Character: Character{Equipment: []Equipment{e}}}},
		"storage":   Storage{Items: []StoredItem{{Equipment: &e}}},
		"cash":      CashMoveResult{Item: CashItem{Equipment: &e}, Character: Character{Equipment: []Equipment{e}}},
		"script":    ScriptExchangeResult{InventoryResult: InventoryResult{Character: Character{Equipment: []Equipment{e}}}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"raw_state":255`)) || !bytes.Contains(raw, []byte(`"options":[65519,65516,65535,0,17]`)) || !bytes.Contains(raw, []byte(`"enhance":255`)) {
				t.Fatalf("portable metadata missing: %s", raw)
			}
			decoded := reflect.New(reflect.TypeOf(value))
			if err = json.Unmarshal(raw, decoded.Interface()); err != nil || !reflect.DeepEqual(decoded.Elem().Interface(), value) {
				t.Fatalf("portable JSON changed metadata: %s err=%v", raw, err)
			}
		})
	}
	raw, _ := json.Marshal(e)
	var round Equipment
	if err := json.Unmarshal(raw, &round); err != nil || round != e {
		t.Fatalf("opaque roundtrip=%+v err=%v", round, err)
	}
	legacy, _ := json.Marshal(Equipment{ItemID: e.ItemID})
	if bytes.Contains(legacy, []byte(`"potential"`)) || bytes.Contains(legacy, []byte(`"enhance"`)) {
		t.Fatalf("legacy JSON changed: %s", legacy)
	}
	var explicit Equipment
	if err := json.Unmarshal([]byte(`{"potential":{"options":[0,0,0,0,0]}}`), &explicit); err != nil || explicit.Potential.IsZero() || !explicit.Potential.Present {
		t.Fatalf("explicit all-zero metadata lost: %+v %v", explicit.Potential, err)
	}
	raw, _ = json.Marshal(explicit)
	if !bytes.Contains(raw, []byte(`"potential"`)) {
		t.Fatalf("explicit all-zero metadata omitted: %s", raw)
	}
}

func TestEquipmentPotentialRejectsLossyShapeWithoutPartialMutation(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"raw_state":256}`, `{"raw_state":-1}`, `{"flags":65536}`,
		`{"options":[1,2,3,4]}`, `{"options":[1,2,3,4,5,6]}`, `{"options":null}`,
		`{"options":[1,2,3,4,65536]}`, `{"options":[1,2,3,4,-1]}`,
		`{"raw_state":1,"future_option":7}`, `{"raw_state":1} {"raw_state":2}`,
	} {
		old := EquipmentPotential{RawState: 17, Flags: 9}
		got := old
		if err := json.Unmarshal([]byte(raw), &got); err == nil || got != old {
			t.Fatalf("lossy/partial decode raw=%s got=%+v err=%v", raw, got, err)
		}
	}
	previous := EquipmentPotential{Present: true, RawState: 17}
	got := previous
	if err := got.UnmarshalJSON([]byte(`{"raw_state":1} {"raw_state":2}`)); err == nil || got != previous {
		t.Fatal("direct decoder accepted multiple objects or changed old value")
	}
	var sealed EquipmentPotential
	if err := json.Unmarshal([]byte(`{"raw_state":1}`), &sealed); err != nil || sealed.RawState != 1 {
		t.Fatalf("sealed state without options=%+v %v", sealed, err)
	}
}

func TestEquipmentPotentialStandaloneAndAuditRoundtrip(t *testing.T) {
	values := []EquipmentPotential{
		{},
		{Present: true},
		{RawState: 255, Options: [5]uint16{65535, 65519, 1, 2, 3}, Flags: 65535},
		{Present: true, RawState: 1, Options: [5]uint16{65519}},
	}
	for _, before := range values {
		raw, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		var after EquipmentPotential
		if err = json.Unmarshal(raw, &after); err != nil || after != before {
			t.Fatalf("standalone state changed before=%+v after=%+v err=%v", before, after, err)
		}
	}
	type audit struct {
		Before EquipmentPotential `json:"before"`
		After  EquipmentPotential `json:"after"`
	}
	original := audit{After: values[2]}
	raw, _ := json.Marshal(original)
	var decoded audit
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded != original {
		t.Fatalf("audit before/after changed original=%+v decoded=%+v err=%v", original, decoded, err)
	}
	reencoded, _ := json.Marshal(decoded)
	if !bytes.Equal(raw, reencoded) {
		t.Fatalf("audit encoding unstable original=%s decoded=%s", raw, reencoded)
	}
}
