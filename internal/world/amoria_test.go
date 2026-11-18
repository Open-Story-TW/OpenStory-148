package world

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestAmoriaTransitionKeepsActiveNPCScriptUnlocked(t *testing.T) {
	f := eventTestFixture(t)
	s := f.server
	maps := filepath.Join(os.Getenv("MAPLEGO_WZ_ROOT"), "Map.wz", "Map", "Map6")
	if err := os.MkdirAll(maps, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{content.AmoriaCodes, content.AmoriaGates} {
		name := fmt.Sprintf("%d.img", id)
		gates := ""
		if id == content.AmoriaGates {
			gates = `<imgdir name="2"><string name="pn" value="gt00PIA"/><int name="pt" value="1"/><int name="x" value="-8190"/><int name="y" value="572"/><int name="tm" value="670010600"/><string name="tn" value="gt00PIB"/></imgdir>
			<imgdir name="3"><string name="pn" value="gt00PIB"/><int name="pt" value="1"/><int name="x" value="-8110"/><int name="y" value="435"/></imgdir>`
		}
		data := fmt.Sprintf(`<imgdir name="%s"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="12"/><int name="y" value="34"/></imgdir><imgdir name="1"><string name="pn" value="st00"/><int name="pt" value="1"/><int name="x" value="12"/><int name="y" value="34"/></imgdir>%s</imgdir></imgdir>`, name, gates)
		if err := os.WriteFile(filepath.Join(maps, name+".xml"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	definition, _ := content.EventByMap(content.AmoriaCodes)
	inst := &eventInstance{
		id: "amoria-transition", channel: 1, definition: definition,
		members: make(map[int64]struct{}), deadline: time.Now().Add(time.Minute), amoria: newAmoriaState(),
	}
	s.events.running[eventKey{1, content.AmoriaName}] = inst
	party := domain.PartySnapshot{ID: 50, LeaderID: 1}
	for id := int64(1); id <= 6; id++ {
		party.Members = append(party.Members, domain.PartyMember{CharacterID: id})
	}
	for _, member := range party.Members {
		id := member.CharacterID
		inst.members[id], s.events.byMember[id] = struct{}{}, inst
		f.join(t, id, 40, content.AmoriaCodes, party)
	}
	leader := eventOccupant{f.lives[1], f.sinks[1]}
	conversation := &worldScript{live: leader.live}
	s.scripts.active[1] = conversation
	completed := make(chan error, 1)
	go func() {
		// startScriptAtNPC retains this mutex while dispatching the warp.
		conversation.mu.Lock()
		defer conversation.mu.Unlock()
		completed <- s.warpAmoria(context.Background(), inst, leader, content.AmoriaCodes, content.AmoriaGates, "st00", true)
	}()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Amoria warp blocked on the active NPC conversation")
	}
	for _, member := range party.Members {
		if got := f.mapOf(member.CharacterID); got != content.AmoriaGates {
			t.Errorf("character %d remained in %d", member.CharacterID, got)
		}
		sink := f.sinks[member.CharacterID]
		sink.mu.Lock()
		actionsReleased := slices.ContainsFunc(sink.packets, func(packet []byte) bool {
			return bytes.Equal(packet, protocol.EnableActions())
		})
		sink.mu.Unlock()
		if !actionsReleased {
			t.Errorf("character %d did not receive the action release after the warp", member.CharacterID)
		}
	}
	t.Run("linked gate landing", func(t *testing.T) {
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[mapKey{1, content.AmoriaGates}]
		room.reactors = map[int32]*roomReactor{1: {state: 4, def: content.Reactor{Index: 0, TemplateID: 6702001}}}
		s.gameplay.mu.Unlock()
		portal := content.Portal{Name: "gt00PCS", Type: 9, Script: "doorgo"}
		handled, err := s.amoriaPortal(context.Background(), 1, leader.live, leader.sink, portal)
		if !handled || err != nil {
			t.Fatalf("Amoria gate was not handled: %v, %v", handled, err)
		}
		actor := partySession(leader.live).Character
		if actor.X != -8110 || actor.Y != 435 {
			t.Fatalf("gate landing was (%d,%d), want (-8110,435)", actor.X, actor.Y)
		}
	})
}

func TestAmoriaFairyHammerLandsInSharedHall(t *testing.T) {
	root := t.TempDir()
	maps := filepath.Join(root, "Map.wz", "Map", "Map6")
	if err := os.MkdirAll(maps, 0700); err != nil {
		t.Fatal(err)
	}
	const data = `<imgdir name="670010200.img"><imgdir name="info"/>
	<imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir>
	<imgdir name="foothold"><imgdir name="0"><imgdir name="0"><imgdir name="1">
	<int name="x1" value="-700"/><int name="x2" value="700"/><int name="y1" value="810"/><int name="y2" value="810"/>
	</imgdir></imgdir></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(maps, "670010200.img.xml"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: &gameplaySpace{catalog: catalog}}
	ticket := &combatTicket{key: mapKey{mapID: content.AmoriaMirror}}
	ticket.request.Targets = []domain.CombatTarget{
		{MobID: content.AmoriaFairy, X: -57, Y: 1007, Defeated: true},
		{MobID: content.AmoriaFairy, X: -1000, Y: 980, Defeated: true},
		{MobID: 9400533, X: 330, Y: 1020, Defeated: true},
	}
	s.placeAmoriaFairyLoot(ticket)
	for i, want := range [][2]int{{-57, 810}, {amoriaFairyX, 810}, {330, 1020}} {
		target := ticket.request.Targets[i]
		if got := [2]int{target.X, target.Y}; got != want {
			t.Errorf("target %d loot origin = %v, want %v", i, got, want)
		}
	}
}
