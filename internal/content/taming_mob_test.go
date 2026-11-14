package content

import (
	"os"
	"testing"
)

func TestTamingMobDefinitionsUseMountSlots(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("needs MAPLEGO_WZ_ROOT")
	}
	for id, slot := range map[int]int{1902000: -18, 1912000: -19} {
		item, err := ItemDefinition(root, id)
		if err != nil || item.InvType != 1 || !item.AllowsSlot(slot) {
			t.Fatalf("%d: %+v %v", id, item, err)
		}
	}
}
