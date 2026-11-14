package world

import (
	"testing"

	"maple-go-server/internal/protocol"
)

// Amherst 11100 has no WZ info/script; unless the server registers it the
// native client never sends NPCTalk and the shop cannot open.
func TestShopNPCsAreRegisteredScriptable(t *testing.T) {
	gameplay, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	room, err := gameplay.room(mapKey{channel: 1, mapID: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	packets := (&Server{}).npcScriptability(room.npcs)
	if len(packets) != 1 {
		t.Fatalf("scriptable packets = %d", len(packets))
	}
	r := protocol.NewReader(packets[0])
	if r.U16() != 0x1db {
		t.Fatal("wrong opcode")
	}
	registered := map[int32]bool{}
	for n := r.U8(); n > 0; n-- {
		id := r.I32()
		if r.String() == "" || r.I32() > 20261101 || r.I32() < 20261101 {
			t.Fatalf("NPC %d script entry is not active", id)
		}
		registered[id] = true
	}
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("malformed scriptable packet")
	}
	for _, id := range []int32{11000, 11100} {
		if !registered[id] {
			t.Fatalf("shop NPC %d not registered: %v", id, registered)
		}
	}
	if registered[12000] {
		t.Fatal("NPC without shop or script registered")
	}
}
