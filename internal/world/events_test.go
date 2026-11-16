package world

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

type eventFixture struct {
	server *Server
	lives  map[int64]*liveCharacter
	sinks  map[int64]*gameplayTestSink
}

// eventTestFixture puts a party of the given levels on the Kerning lobby map;
// the first character leads. Extra characters outside the party are added
// with join.
func eventTestFixture(t *testing.T, levels ...int) *eventFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "Map.wz/Map/Map9"), 0700); err != nil {
		t.Fatal(err)
	}
	for id := 910340000; id <= 910340700; id += 100 {
		name := strconv.Itoa(id) + ".img"
		data := `<imgdir name="` + name + `"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="10"/><int name="y" value="20"/></imgdir></imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(root, "Map.wz/Map/Map9", name+".xml"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "Mob.wz"), 0700); err != nil {
		t.Fatal(err)
	}
	mob := `<imgdir name="9300003.img"><imgdir name="info"><int name="maxHP" value="23000"/><int name="boss" value="1"/></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Mob.wz", "9300003.img.xml"), []byte(mob), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	if g.catalog, err = content.NewCatalog(root); err != nil {
		t.Fatal(err)
	}
	scripts, err := newScriptWorld()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: g, scripts: scripts, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{},
		parties: newPartySpace(), events: newEventSpace()}
	s.repo = gameplayTestRepo{save: func(domain.CharacterSession, domain.Character) error { return nil }}
	f := &eventFixture{server: s, lives: map[int64]*liveCharacter{}, sinks: map[int64]*gameplayTestSink{}}
	party := domain.PartySnapshot{ID: 50, LeaderID: 1}
	for i := range levels {
		party.Members = append(party.Members, domain.PartyMember{CharacterID: int64(i + 1)})
	}
	for i, level := range levels {
		f.join(t, int64(i+1), level, 910340700, party)
	}
	return f
}

func (f *eventFixture) join(t *testing.T, id int64, level, mapID int, party domain.PartySnapshot) {
	t.Helper()
	s := f.server
	live := &liveCharacter{session: domain.CharacterSession{AccountID: id, CharacterID: id, SessionID: "event-session-" + strconv.FormatInt(id, 10), Generation: 1, ChannelID: 1,
		Character: domain.Character{ID: id, MapID: mapID, Level: level, HP: 50, MaxHP: 50, Revision: 1}}}
	sink := &gameplayTestSink{}
	s.online[id] = live.session
	seedBoundScene(t, s, live, sink)
	if err := s.enterGameplay(1, live, sink); err != nil {
		t.Fatal(err)
	}
	s.parties.peers[id] = newPartyPeer(live, sink, party)
	f.lives[id], f.sinks[id] = live, sink
}

func (f *eventFixture) mapOf(id int64) int { return partySession(f.lives[id]).Character.MapID }

// settle waits for the warps an ending event runs on its own goroutine.
func (f *eventFixture) settle(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatal("event did not settle")
}

func (f *eventFixture) clocks(id int64) int {
	sink := f.sinks[id]
	sink.mu.Lock()
	defer sink.mu.Unlock()
	n := 0
	for _, p := range sink.packets {
		if len(p) == 7 && binary.LittleEndian.Uint16(p) == 0xE5 && p[2] == 2 {
			n++
		}
	}
	return n
}

func TestEventStartMovesThePartyAndHoldsTheMaps(t *testing.T) {
	f := eventTestFixture(t, 30, 25, 40)
	s := f.server
	reason, here, qualified, err := s.eventStart(context.Background(), "KerningPQ", 1, f.lives[1], f.sinks[1])
	if reason != "" || err != nil || here != 3 || qualified != 3 {
		t.Fatalf("reason=%q here=%d qualified=%d err=%v", reason, here, qualified, err)
	}
	for id := int64(1); id <= 3; id++ {
		if f.mapOf(id) != 910340100 || f.clocks(id) != 1 || s.eventOf(id) == nil {
			t.Fatalf("member %d: map=%d clocks=%d", id, f.mapOf(id), f.clocks(id))
		}
	}
	if status := s.eventStatus(1, "KerningPQ"); status["busy"] != true || status["members"] != int64(3) {
		t.Fatalf("status %v", status)
	}
	// A second party cannot enter while the first is inside.
	other := domain.PartySnapshot{ID: 51, LeaderID: 8, Members: []domain.PartyMember{{CharacterID: 8}, {CharacterID: 9}, {CharacterID: 10}}}
	f.join(t, 8, 30, 910340700, other)
	f.join(t, 9, 30, 910340700, other)
	f.join(t, 10, 30, 910340700, other)
	if reason, _, _, _ = s.eventStart(context.Background(), "KerningPQ", 1, f.lives[8], f.sinks[8]); reason != "busy" || f.mapOf(8) != 910340700 {
		t.Fatalf("second party: reason=%q map=%d", reason, f.mapOf(8))
	}
	// Properties belong to the running group only.
	if err = s.setEventProperty(1, "stage", "2"); err != nil || s.eventProperty(2, "stage") != "2" || s.eventProperty(8, "stage") != "" || s.setEventProperty(8, "stage", "9") == nil {
		t.Fatalf("properties err=%v", err)
	}
	// Moving between stage maps keeps membership.
	stage, _ := s.gameplay.catalog.Map(910340200)
	if err = s.warpGameplay(context.Background(), 1, f.lives[2], f.sinks[2], 910340200, stage.Portals[0]); err != nil || s.eventOf(2) == nil || f.clocks(2) != 2 {
		t.Fatalf("stage move err=%v clocks=%d", err, f.clocks(2))
	}
}

func TestEventStartRefusals(t *testing.T) {
	cases := []struct {
		name   string
		levels []int
		setup  func(*eventFixture)
		leader int64
		want   string
	}{
		{"alone", []int{30}, nil, 1, "party_size"},
		{"two-members", []int{30, 30}, nil, 1, "party_size"},
		{"level", []int{30, 10, 30}, nil, 1, "level"},
		{"not-leader", []int{30, 30, 30}, nil, 2, "not_leader"},
		{"no-party", []int{30, 30, 30}, func(f *eventFixture) { f.server.parties.peers[1].party = domain.PartySnapshot{} }, 1, "no_party"},
		{"member-elsewhere", []int{30, 30, 30}, func(f *eventFixture) { f.lives[2].session.Character.MapID = 100000000 }, 1, "party_size"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := eventTestFixture(t, c.levels...)
			if c.setup != nil {
				c.setup(f)
			}
			reason, _, _, err := f.server.eventStart(context.Background(), "KerningPQ", 1, f.lives[c.leader], f.sinks[c.leader])
			if reason != c.want || err != nil || f.mapOf(1) != 910340700 || f.server.eventOf(1) != nil {
				t.Fatalf("reason=%q err=%v map=%d", reason, err, f.mapOf(1))
			}
		})
	}
	if reason, _, _, _ := eventTestFixture(t, 30, 30).server.eventStart(context.Background(), "NoSuchEvent", 1, nil, nil); reason != "unknown" {
		t.Fatalf("unknown event: %q", reason)
	}
}

func (f *eventFixture) started(t *testing.T) *eventInstance {
	t.Helper()
	if reason, _, _, err := f.server.eventStart(context.Background(), "KerningPQ", 1, f.lives[1], f.sinks[1]); reason != "" || err != nil {
		t.Fatalf("start: %q %v", reason, err)
	}
	return f.server.eventOf(1)
}

func TestKerningStagePortalRequiresClear(t *testing.T) {
	f := eventTestFixture(t, 30, 30, 30)
	s := f.server
	inst := f.started(t)
	p := content.Portal{Name: "next000", Script: "party_portal"}
	for stage := 1; stage <= 4; stage++ {
		from := inst.definition.Maps[stage-1]
		next := inst.definition.Maps[stage]
		use := func(id int64, want int) {
			t.Helper()
			live, sink := f.lives[id], f.sinks[id]
			handled, err := s.kerningStagePortal(context.Background(), partySession(live), live, sink, p)
			if !handled || err != nil || f.mapOf(id) != want {
				t.Fatalf("stage %d player %d handled=%v map=%d err=%v", stage, id, handled, f.mapOf(id), err)
			}
		}
		use(1, from)
		if err := s.setEventProperty(1, strconv.Itoa(stage)+"stageclear", "true"); err != nil {
			t.Fatal(err)
		}
		use(1, next)
		use(2, next)
	}
	if s.mapMonsterCount(inst.channel, inst.definition.Maps[4]) != 1 {
		t.Fatal("King Slime was not spawned in the final room")
	}
}

func (f *eventFixture) allOut(ids ...int64) func() bool {
	return func() bool {
		for _, id := range ids {
			if f.mapOf(id) != 910340000 || f.server.eventOf(id) != nil {
				return false
			}
		}
		return f.server.eventStatus(1, "KerningPQ")["busy"] == false
	}
}

func TestEventEndsAtItsTimeLimit(t *testing.T) {
	f := eventTestFixture(t, 30, 30, 30)
	inst := f.started(t)
	f.server.tickEvents(time.Now())
	if f.mapOf(1) != 910340100 {
		t.Fatal("ended before the deadline")
	}
	f.server.tickEvents(inst.deadline)
	f.settle(t, f.allOut(1, 2, 3))
}

func TestEventEndsWhenTooFewRemain(t *testing.T) {
	for _, way := range []string{"disconnect", "walk-out", "leave-party"} {
		t.Run(way, func(t *testing.T) {
			f := eventTestFixture(t, 30, 30, 30)
			inst := f.started(t)
			s := f.server
			exit, _ := s.gameplay.catalog.Map(910340000)
			// Three members and Keep 2: the first departure changes nothing.
			if err := s.warpGameplay(context.Background(), 1, f.lives[3], f.sinks[3], 910340000, exit.Portals[0]); err != nil || s.eventOf(3) != nil || s.eventOf(1) == nil {
				t.Fatalf("first departure err=%v", err)
			}
			switch way {
			case "disconnect":
				s.eventDeparted(2)
				f.settle(t, f.allOut(1))
			case "walk-out":
				if err := s.warpGameplay(context.Background(), 1, f.lives[2], f.sinks[2], 910340000, exit.Portals[0]); err != nil {
					t.Fatal(err)
				}
				f.settle(t, f.allOut(1, 2))
			case "leave-party":
				s.eventPartyLeft(inst.partyID, []int64{2})
				f.settle(t, f.allOut(1, 2))
			}
		})
	}
}

func TestEventFinishAndStrangers(t *testing.T) {
	f := eventTestFixture(t, 30, 30, 30)
	inst := f.started(t)
	s := f.server
	// A character who is no member and lands on a stage map is sent out; a
	// GM may stay.
	f.join(t, 8, 30, 910340100, domain.PartySnapshot{})
	f.join(t, 9, 30, 910340100, domain.PartySnapshot{})
	f.lives[9].session.Character.GM = 1
	s.eventEntered(1, f.lives[8], f.sinks[8])
	s.eventEntered(1, f.lives[9], f.sinks[9])
	f.settle(t, func() bool { return f.mapOf(8) == 910340000 })
	if f.mapOf(9) != 910340100 || f.mapOf(1) != 910340100 {
		t.Fatalf("gm=%d leader=%d", f.mapOf(9), f.mapOf(1))
	}
	s.eventEnd(inst)
	s.eventEnd(inst) // ending twice is harmless
	f.settle(t, f.allOut(1, 2, 3))
	f.settle(t, func() bool { return f.mapOf(9) == 910340000 })
	// The maps are free again.
	if reason, _, _, err := s.eventStart(context.Background(), "KerningPQ", 1, f.lives[1], f.sinks[1]); reason != "" || err != nil || f.mapOf(2) != 910340100 {
		t.Fatalf("restart: %q %v", reason, err)
	}
}
