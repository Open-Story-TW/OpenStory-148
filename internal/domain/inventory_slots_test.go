package domain

import (
	"encoding/json"
	"testing"
)

func TestInventoryCapacityDefaultsBoundsAndJSON(t *testing.T) {
	c := Character{}
	for invType := 1; invType <= 5; invType++ {
		if c.Capacity(invType) != 24 {
			t.Fatal("starter capacity changed")
		}
	}
	if c.Capacity(0) != 0 || c.Capacity(6) != 0 {
		t.Fatal("invalid inventory type accepted")
	}
	c.InventorySlots = [5]int{28, 96, 24, 28, 0}
	if c.Capacity(1) != 28 || c.Capacity(2) != 96 || c.Capacity(5) != 24 {
		t.Fatal("independent bag capacity lost")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var round Character
	if err = json.Unmarshal(raw, &round); err != nil || round.InventorySlots != c.InventorySlots {
		t.Fatal("capacity snapshot roundtrip", err)
	}
	if RosterView(c).InventorySlots != [5]int{} {
		t.Fatal("capacity metadata leaked into roster")
	}
	c.InventorySlots[0] = 97
	if c.Capacity(1) != 0 {
		t.Fatal("oversized capacity accepted")
	}
}
