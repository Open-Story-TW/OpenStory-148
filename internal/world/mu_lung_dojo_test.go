package world

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// dojoTestFixture writes the hall, the exit room, rooms 0 and 1 of every
// floor (portals and the door as in v148 Map.wz) and the forty bosses, and
// puts solo characters in the hall.
func dojoTestFixture(t *testing.T, ids ...int64) *eventFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	write := func(rel, data string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	portal := func(i int, name string, kind int, x, y int, script string) string {
		return `<imgdir name="` + strconv.Itoa(i) + `"><string name="pn" value="` + name + `"/><int name="pt" value="` + strconv.Itoa(kind) +
			`"/><int name="x" value="` + strconv.Itoa(x) + `"/><int name="y" value="` + strconv.Itoa(y) +
			`"/><int name="tm" value="999999999"/><string name="tn" value=""/><string name="script" value="` + script + `"/></imgdir>`
	}
	mapXML := func(id int, body string) {
		name := strconv.Itoa(id) + ".img"
		write("Map.wz/Map/Map9/"+name+".xml", `<imgdir name="`+name+`"><imgdir name="info"><int name="returnMap" value="925020002"/></imgdir>`+body+`</imgdir>`)
	}
	mapXML(content.DojoHall, `<imgdir name="portal">`+portal(0, "sp", 0, -353, -45, "")+`</imgdir>`+
		`<imgdir name="life"><imgdir name="0"><string name="type" value="n"/><string name="id" value="2091005"/><int name="x" value="2"/><int name="y" value="-149"/><int name="fh" value="8"/></imgdir></imgdir>`)
	mapXML(content.DojoExit, `<imgdir name="portal">`+portal(0, "sp", 0, -352, -125, "")+`</imgdir>`)
	for floor := 1; floor <= content.DojoFloors; floor++ {
		for room := 0; room < 2; room++ {
			portals := portal(0, "sp", 0, -391, -68, "") + portal(1, "out001", 8, 6, -367, "dojang_next")
			body := `<imgdir name="foothold"><imgdir name="0"><imgdir name="0"><imgdir name="7"><int name="x1" value="235"/><int name="y1" value="7"/><int name="x2" value="502"/><int name="y2" value="7"/></imgdir></imgdir></imgdir></imgdir>`
			if !content.DojoRestFloor(floor) {
				portals += portal(2, "out00", 7, 429, 7, "dojang_up")
				body += `<imgdir name="reactor"><imgdir name="0"><string name="id" value="2508000"/><int name="x" value="1"/><int name="y" value="-364"/><string name="name" value="door"/></imgdir></imgdir>`
			}
			mapXML(content.DojoFloorMap(floor, room), `<imgdir name="portal">`+portals+`</imgdir>`+body)
		}
	}
	for stage := 0; stage < content.DojoStages; stage++ {
		id := strconv.Itoa(content.DojoFirstBoss + stage)
		write("Mob.wz/"+id+".img.xml", `<imgdir name="`+id+`.img"><imgdir name="info"><int name="maxHP" value="52500"/><int name="boss" value="1"/></imgdir></imgdir>`)
	}
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	if g.catalog, err = content.NewCatalog(root); err != nil {
		t.Fatal(err)
	}
	s := &Server{gameplay: g, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{},
		parties: newPartySpace(), events: newEventSpace()}
	s.repo = gameplayTestRepo{save: func(domain.CharacterSession, domain.Character) error { return nil }}
	f := &eventFixture{server: s, lives: map[int64]*liveCharacter{}, sinks: map[int64]*gameplayTestSink{}}
	for _, id := range ids {
		f.join(t, id, 30, content.DojoHall, domain.PartySnapshot{})
	}
	return f
}

func (f *eventFixture) sent(id int64, packet []byte) int {
	sink := f.sinks[id]
	sink.mu.Lock()
	defer sink.mu.Unlock()
	n := 0
	for _, p := range sink.packets {
		if bytes.Equal(p, packet) {
			n++
		}
	}
	return n
}

func (f *eventFixture) forget(id int64) {
	sink := f.sinks[id]
	sink.mu.Lock()
	sink.packets = nil
	sink.mu.Unlock()
}

func (f *eventFixture) dojoStarted(t *testing.T, id int64) *eventInstance {
	t.Helper()
	reason, here, _, err := f.server.dojoStart(context.Background(), 1, f.lives[id], f.sinks[id])
	if reason != "" || err != nil || here != 1 {
		t.Fatalf("start: %q here=%d err=%v", reason, here, err)
	}
	inst := f.server.eventOf(id)
	if inst == nil || inst.dojo == nil {
		t.Fatal("no dojo run")
	}
	return inst
}

// clearFloor kills the floor's boss the way a settled attack reports it.
func (f *eventFixture) clearFloor(t *testing.T, id int64) {
	t.Helper()
	mapID := f.mapOf(id)
	floor, _, _ := content.DojoFloorOf(mapID)
	if boss, ok := content.DojoBoss(floor); ok {
		f.server.dojoMobDefeated(mapKey{1, mapID}, boss)
	}
}

func (f *eventFixture) usePortal(t *testing.T, id int64, name string) {
	t.Helper()
	field, err := f.server.gameplay.catalog.Map(f.mapOf(id))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := field.Portal(name)
	if !ok {
		t.Fatalf("map %d has no %s", field.ID, name)
	}
	handled, err := f.server.dojoPortal(context.Background(), 1, f.lives[id], f.sinks[id], p)
	if !handled || err != nil {
		t.Fatalf("portal %s on %d: handled=%v err=%v", name, field.ID, handled, err)
	}
}

// doorOpened reports that the floor's door reactor is open and the member
// saw it open.
func (f *eventFixture) doorOpened(t *testing.T, id int64) bool {
	t.Helper()
	g := f.server.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{1, f.mapOf(id)}]
	for _, door := range room.reactors {
		if door.def.TemplateID == content.DojoDoorReactor {
			return door.state == 1 && f.sent(id, protocol.ReactorState(door.objectID, 1, door.def.X, door.def.Y)) == 1
		}
	}
	return false
}

func TestDojoStartShowsTheFirstFloor(t *testing.T) {
	f := dojoTestFixture(t, 1, 2)
	s := f.server
	inst := f.dojoStarted(t, 1)
	if f.mapOf(1) != 925020100 || inst.definition.Name != content.DojoEventName(0) {
		t.Fatalf("map %d event %s", f.mapOf(1), inst.definition.Name)
	}
	for _, p := range [][]byte{
		protocol.QuestRecord(7214, "0"),
		protocol.FieldScreenEffect("dojang/start/stage"),
		protocol.FieldScreenEffect("dojang/start/number/1"),
		protocol.Clock(content.DojoFloorSeconds(1)),
		protocol.SessionValue("energy", "0"),
		protocol.MapValue("Bamboo_Used", "0"),
	} {
		if f.sent(1, p) != 1 {
			t.Fatalf("missing %x", p)
		}
	}
	if s.mapMonsterCount(1, 925020100) != 1 {
		t.Fatal("no boss on floor 1")
	}
	// The next challenger gets the next room.
	f.dojoStarted(t, 2)
	if f.mapOf(2) != 925020101 || s.eventOf(2) == inst {
		t.Fatalf("second challenger map %d", f.mapOf(2))
	}
	// A room name cannot be started directly, and a stranger cannot walk in.
	if s.eventAllowsEntry(partySession(f.lives[2]), 925020100) || !s.eventAllowsEntry(partySession(f.lives[2]), 925020201) {
		t.Fatal("room membership")
	}
}

func TestDojoClimbsEveryFloorAndFinishes(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	inst := f.dojoStarted(t, 1)
	run := inst.dojo
	// The hidden portal waits for the boss.
	f.usePortal(t, 1, "out001")
	if f.mapOf(1) != 925020100 {
		t.Fatal("left floor 1 before the boss died")
	}
	elapsed := 0
	for floor := 1; floor <= content.DojoFloors; floor++ {
		if f.mapOf(1) != content.DojoFloorMap(floor, 0) || run.floor != floor {
			t.Fatalf("floor %d: map %d run %d", floor, f.mapOf(1), run.floor)
		}
		if stage := content.DojoStage(floor); stage > 0 {
			if f.sent(1, protocol.FieldScreenEffect("dojang/start/number/"+strconv.Itoa(stage))) != 1 || f.sent(1, protocol.MapValue("Bamboo_Used", "0")) != 1 {
				t.Fatalf("floor %d stage %d HUD", floor, stage)
			}
			if run.cleared {
				t.Fatalf("boss floor %d starts cleared", floor)
			}
			f.clearFloor(t, 1)
			if !run.cleared || f.sent(1, protocol.FieldScreenEffect("dojang/end/clear")) != 1 || !f.doorOpened(t, 1) {
				t.Fatalf("floor %d not cleared", floor)
			}
		} else if !run.cleared || f.sent(1, protocol.FieldScreenEffect("dojang/start/stage")) != 0 || f.sent(1, protocol.MapValue("Bamboo_Used", "0")) != 0 {
			t.Fatalf("rest floor %d HUD", floor)
		}
		// Thirty seconds on each floor add up in quest record 7214.
		run.mu.Lock()
		run.floorStart = run.floorStart.Add(-30 * time.Second)
		run.mu.Unlock()
		f.forget(1)
		f.usePortal(t, 1, "out001")
		if floor < content.DojoFloors {
			elapsed += 30
			clocks := 1
			if content.DojoRestFloor(floor + 1) {
				clocks = 0 // a rest floor shows no clock
			}
			if f.sent(1, protocol.QuestRecord(7214, strconv.Itoa(elapsed))) != 1 || f.sent(1, protocol.Clock(content.DojoFloorSeconds(floor+1))) != clocks {
				t.Fatalf("floor %d: elapsed record %d or clock missing", floor+1, elapsed)
			}
			if left := time.Until(inst.currentDeadline()); left < time.Duration(content.DojoFloorSeconds(floor+1))*time.Second {
				t.Fatalf("floor %d deadline %v", floor+1, left)
			}
		}
	}
	f.settle(t, func() bool { return f.mapOf(1) == content.DojoExit && s.eventOf(1) == nil })
}

func TestDojoTimeOutAndLeaving(t *testing.T) {
	f := dojoTestFixture(t, 1, 2)
	s := f.server
	inst := f.dojoStarted(t, 1)
	s.tickEvents(time.Now().Add(time.Duration(content.DojoFloorSeconds(1)) * time.Second))
	if f.mapOf(1) != 925020100 {
		t.Fatal("the time-over grace was not kept")
	}
	s.tickEvents(inst.currentDeadline())
	f.settle(t, func() bool { return f.mapOf(1) == content.DojoExit && s.eventOf(1) == nil })

	f.dojoStarted(t, 2)
	f.usePortal(t, 2, "out00")
	f.settle(t, func() bool { return f.mapOf(2) == content.DojoExit && s.eventOf(2) == nil })
	// A new challenge, from the hall again, starts from zero.
	if _, _, _, err := s.dojoStart(context.Background(), 1, f.lives[2], f.sinks[2]); err != nil || s.eventOf(2) != nil {
		t.Fatal("started outside the hall")
	}
	hall, _ := s.gameplay.catalog.Map(content.DojoHall)
	if err := s.warpGameplay(context.Background(), 1, f.lives[2], f.sinks[2], hall.ID, hall.Portals[0]); err != nil {
		t.Fatal(err)
	}
	again := f.dojoStarted(t, 2)
	if again.dojo.floor != 1 || again.dojo.elapsed != 0 || f.sent(2, protocol.QuestRecord(7214, "0")) != 2 {
		t.Fatalf("restart %+v", again.dojo)
	}
}

func dojoTicket(f *eventFixture, id int64, skillID int, damage ...int) *combatTicket {
	session := partySession(f.lives[id])
	t := &combatTicket{key: mapKey{1, session.Character.MapID}, actorSession: session, sink: f.sinks[id]}
	t.request.SkillID = skillID
	for _, d := range damage {
		t.request.Targets = append(t.request.Targets, domain.CombatTarget{Damage: d})
	}
	return t
}

func TestDojoEnergyFillsFromDamagingAttacks(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	run := f.dojoStarted(t, 1).dojo
	s.dojoAttackSettled(dojoTicket(f, 1, 0, 0))
	s.dojoAttackSettled(dojoTicket(f, 1, 1009, 500))
	contact := dojoTicket(f, 1, 0, 9)
	contact.monsterContact = []byte{1}
	s.dojoAttackSettled(contact)
	if run.energy[1] != 0 {
		t.Fatalf("energy %d from misses, Bamboo Rain or contact", run.energy[1])
	}
	s.dojoAttackSettled(dojoTicket(f, 1, 0, 0, 7))
	if run.energy[1] != content.DojoEnergyPerHit || f.sent(1, protocol.SessionValue("energy", "100")) != 1 {
		t.Fatalf("energy %d", run.energy[1])
	}
	for i := 0; i < 150; i++ {
		s.dojoAttackSettled(dojoTicket(f, 1, 1001004, 3))
	}
	if run.energy[1] != content.DojoEnergyFull || f.sent(1, protocol.SessionValue("energy", "10000")) != 1 {
		t.Fatalf("energy %d not capped", run.energy[1])
	}
}

func TestDojoSkillsSpendAFullGauge(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	run := f.dojoStarted(t, 1).dojo
	c := partySession(f.lives[1]).Character
	if s.dojoSkillRun(c, 1010) != nil {
		t.Fatal("empty gauge accepted")
	}
	run.energy[1] = content.DojoEnergyFull - 1
	if s.dojoSkillRun(c, 1011) != nil {
		t.Fatal("9999 accepted")
	}
	run.energy[1] = content.DojoEnergyFull
	if s.dojoSkillRun(c, 10001010) != nil {
		t.Fatal("another book's skill passed the dojo gate")
	}
	if !s.dojoSpendSkill(1, c, f.sinks[1], s.dojoSkillRun(c, 1010), 1010) || run.energy[1] != 0 || run.bamboo || f.sent(1, protocol.SessionValue("energy", "0")) != 2 {
		t.Fatalf("1010 spend: energy %d bamboo %v", run.energy[1], run.bamboo)
	}
	run.energy[1] = content.DojoEnergyFull
	if !s.dojoSpendSkill(1, c, f.sinks[1], s.dojoSkillRun(c, 1009), 1009) || !run.bamboo || f.sent(1, protocol.MapValue("Bamboo_Used", "1")) != 1 {
		t.Fatal("1009 spend")
	}
	run.energy[1] = content.DojoEnergyFull
	if s.dojoSkillRun(c, 1009) != nil {
		t.Fatal("second Bamboo Rain on one floor")
	}
	// The flag belongs to the floor: the next floor offers it again.
	f.clearFloor(t, 1)
	f.usePortal(t, 1, "out001")
	c = partySession(f.lives[1]).Character
	if run.bamboo || s.dojoSkillRun(c, 1009) == nil || run.energy[1] != content.DojoEnergyFull {
		t.Fatalf("floor 2: bamboo %v energy %d", run.bamboo, run.energy[1])
	}
	// Outside a Dojang floor nothing is offered.
	c.MapID = content.DojoHall
	if s.dojoSkillRun(c, 1011) != nil {
		t.Fatal("hall accepted")
	}
}

func TestDojoBuffCastNeedsEnergy(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	run := f.dojoStarted(t, 1).dojo
	s.skills = newSkillSpace()
	if err := s.enterSkills(context.Background(), 1, f.lives[1], f.sinks[1]); err != nil {
		t.Fatal(err)
	}
	casts := 0
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		casts++
		c := q.Session.Character
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: q.SkillID, Level: 1, Stat: "DIVINE_BODY", Value: 1, ExpiresAt: time.Now().Add(30 * time.Second)}}
		return domain.SkillResult{Character: c, Changed: true, Effect: true}, nil
	}}
	if err := s.castSkill(context.Background(), f.lives[1], f.sinks[1], protocol.SkillCast{Tick: 1, SkillID: 1010, Level: 1}); err != nil || casts != 0 {
		t.Fatalf("cast without energy: casts %d err %v", casts, err)
	}
	run.energy[1] = content.DojoEnergyFull
	f.forget(1)
	if err := s.castSkill(context.Background(), f.lives[1], f.sinks[1], protocol.SkillCast{Tick: 2, SkillID: 1010, Level: 1}); err != nil || casts != 1 {
		t.Fatalf("cast with energy: casts %d err %v", casts, err)
	}
	if run.energy[1] != 0 || f.sent(1, protocol.SessionValue("energy", "0")) != 1 {
		t.Fatalf("energy %d after the cast", run.energy[1])
	}
}

func TestOnlySoGongInTheHallOpensADojoRoom(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	g := s.gameplay
	g.mu.Lock()
	var soGong int32
	for oid, npc := range g.rooms[mapKey{1, content.DojoHall}].npcs {
		if npc.TemplateID == content.DojoNPC {
			soGong = oid
		}
	}
	g.mu.Unlock()
	start := func(npcID int, objectID int32, name string) (scripting.CommandResult, error) {
		a := &worldScript{server: s, live: f.lives[1], sink: f.sinks[1], kind: "npc", npcID: npcID, objectID: objectID}
		return a.eventCommand(context.Background(), partySession(f.lives[1]), scripting.Command{Kind: "event_start", Text: name})
	}
	for _, c := range []struct {
		npc  int
		oid  int32
		name string
	}{{content.DojoNPC, soGong, content.DojoEventName(0)}, {2091010, soGong, dojoStartRequest}, {content.DojoNPC, soGong + 1000, dojoStartRequest}} {
		if _, err := start(c.npc, c.oid, c.name); err == nil || s.eventOf(1) != nil {
			t.Fatalf("npc %d %q started a room", c.npc, c.name)
		}
	}
	result, err := start(content.DojoNPC, soGong, dojoStartRequest)
	if value, _ := result.Value.(map[string]any); err != nil || value["ok"] != true || f.mapOf(1) != 925020100 {
		t.Fatalf("So Gong start: %v %v map %d", result.Value, err, f.mapOf(1))
	}
	a := &worldScript{server: s, live: f.lives[1], sink: f.sinks[1], kind: "npc", npcID: content.DojoNPC}
	status, err := a.eventCommand(context.Background(), partySession(f.lives[1]), scripting.Command{Kind: "event_status", Text: dojoStartRequest})
	if value, _ := status.Value.(map[string]any); err != nil || value["inside"] != true {
		t.Fatalf("status %v %v", status.Value, err)
	}
}

func TestQADojoEnergyFillsTheCallersCurrentRun(t *testing.T) {
	f := dojoTestFixture(t, 1)
	s := f.server
	if _, ok := s.qaFillDojoEnergy(f.lives[1].session, f.sinks[1]); ok {
		t.Fatal("filled energy in the hall without a challenge")
	}
	inst := f.dojoStarted(t, 1)
	f.forget(1)
	energy, ok := s.qaFillDojoEnergy(f.lives[1].session, f.sinks[1])
	if !ok || energy != content.DojoEnergyFull || f.sent(1, protocol.SessionValue("energy", strconv.Itoa(content.DojoEnergyFull))) != 1 {
		t.Fatalf("fill: energy %d ok %t", energy, ok)
	}
	inst.dojo.mu.Lock()
	got := inst.dojo.energy[1]
	inst.dojo.mu.Unlock()
	if got != content.DojoEnergyFull {
		t.Fatalf("run energy %d", got)
	}
}
