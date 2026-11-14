package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestSpawnPlayerLevelFollowsTheSwitch(t *testing.T) {
	character := domain.Character{ID: 7, Level: 300, Name: "a"}
	const levelOffset = 2 + 4 // opcode, character ID

	restore := domain.SetIntLevelCapForTest(0)
	defer restore()
	if SpawnPlayer(character, 0) != nil {
		t.Fatal("switch off: a level above the byte must not spawn")
	}
	domain.SetIntLevelCapForTest(999)
	packet := SpawnPlayer(character, 0)
	if packet == nil {
		t.Fatal("switch on: level 300 must spawn")
	}
	if packet[levelOffset] != 255 {
		t.Fatalf("level byte = %d, want the saturated 255", packet[levelOffset])
	}
	character.Level = 1000
	if SpawnPlayer(character, 0) != nil {
		t.Fatal("switch on: a level above the cap must not spawn")
	}
}

func TestSummonOwnerLevelSaturatesWithoutWrapping(t *testing.T) {
	restore := domain.SetIntLevelCapForTest(999)
	defer restore()
	view := SummonView{OwnerID: 1, ObjectID: 2, SkillID: 3, SkillLevel: 1, OwnerLevel: 300}
	packet := SpawnSummon(view)
	const ownerLevelOffset = 2 + 4 + 4 + 4 // opcode, owner, object, skill
	if packet == nil || packet[ownerLevelOffset] != 254 {
		t.Fatalf("owner level packet = %v", packet)
	}
}
