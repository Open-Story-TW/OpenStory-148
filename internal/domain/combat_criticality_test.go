package domain

import (
	"encoding/json"
	"testing"
)

func TestCombatCriticalityPreservesLegacyJSONAndUnknownRaw(t *testing.T) {
	legacy := CombatDamageLine{Raw: 426, Applied: 348}
	b, err := json.Marshal(legacy)
	if err != nil || string(b) != `{"Raw":426,"Applied":348}` {
		t.Fatal("legacy receipt line changed", string(b), err)
	}
	unknown := CombatDamageLine{Raw: 426, Applied: 426, Criticality: CombatCriticalUnknown}
	b, err = json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	var restored CombatDamageLine
	if err = json.Unmarshal(b, &restored); err != nil || restored != unknown {
		t.Fatal("unknown identity/raw lost", restored, err)
	}
	q := CombatAttackRequest{OperationID: "frozen-operation", Targets: []CombatTarget{{Lines: []CombatDamageLine{unknown}}}}
	before, err := CombatManifestHash(q)
	if err != nil {
		t.Fatal(err)
	}
	copyBytes, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var frozen CombatAttackRequest
	if err = json.Unmarshal(copyBytes, &frozen); err != nil {
		t.Fatal(err)
	}
	after, err := CombatManifestHash(frozen)
	if err != nil || before != after || frozen.Targets[0].Lines[0].Raw != 426 {
		t.Fatal("frozen retry identity changed", before, after, err)
	}
}
