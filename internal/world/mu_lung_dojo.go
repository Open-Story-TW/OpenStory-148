package world

import (
	"context"
	"strconv"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// dojoTimeOverGrace keeps the group on a floor a little after its clock
// reaches zero, so the client's own timeOver effect (0x52B3B7) plays before
// the forced return.
const dojoTimeOverGrace = 3 * time.Second

// dojoStartRequest is the event_start name So Gong's script uses; the world
// picks a free instance room for it.
const dojoStartRequest = "MuLungDojo"

// dojoRun is one Mu Lung Dojo challenge. The group climbs one floor at a
// time inside its instance room; the event deadline is the current floor's.
type dojoRun struct {
	mu         sync.Mutex
	floor      int
	floorStart time.Time
	// elapsed is the whole seconds spent on the earlier floors: the value of
	// quest record 7214, which the client adds to the floor's running time.
	elapsed int
	cleared bool // the floor's boss is down, or it is a rest floor.
	bamboo  bool // 竹竿千擊 was used on this floor (map value Bamboo_Used).
	energy  map[int64]int
}

func newDojoRun(now time.Time) *dojoRun {
	return &dojoRun{floor: 1, floorStart: now, energy: map[int64]int{}}
}

func dojoFloorDeadline(start time.Time, floor int) time.Time {
	return start.Add(time.Duration(content.DojoFloorSeconds(floor))*time.Second + dojoTimeOverGrace)
}

// dojoStart moves the character (and the party members on the hall map when
// it leads a party) into the first free instance room.
func (s *Server) dojoStart(ctx context.Context, channel int, live *liveCharacter, sink packetSink) (reason string, here, qualified int, err error) {
	for room := 0; room < content.DojoRooms; room++ {
		reason, here, qualified, err = s.eventStart(ctx, content.DojoEventName(room), channel, live, sink)
		if reason != "busy" {
			return reason, here, qualified, err
		}
	}
	return "busy", here, qualified, nil
}

// dojoInstance is the running challenge that holds a floor map.
func (s *Server) dojoInstance(channel, mapID int) *eventInstance {
	if s.events == nil || !content.DojoField(mapID) {
		return nil
	}
	e, ok := content.EventByMap(mapID)
	if !ok {
		return nil
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst := s.events.running[eventKey{channel, e.Name}]
	if inst == nil || inst.dojo == nil || inst.ended {
		return nil
	}
	return inst
}

// dojoEntered shows a member arriving on the current floor its HUD: the
// elapsed record first (the clock handler reads it), the stage effects, the
// floor clock, the energy gauge and the once-per-floor flag.
func (s *Server) dojoEntered(inst *eventInstance, c domain.Character, sink packetSink) {
	floor, _, ok := content.DojoFloorOf(c.MapID)
	run := inst.dojo
	run.mu.Lock()
	if !ok || floor != run.floor {
		run.mu.Unlock()
		return
	}
	elapsed, energy, bamboo := run.elapsed, run.energy[c.ID], run.bamboo
	left := content.DojoFloorSeconds(floor) - int(time.Since(run.floorStart)/time.Second)
	run.mu.Unlock()
	packets := [][]byte{protocol.QuestRecord(content.DojoElapsedRecord, strconv.Itoa(elapsed))}
	if stage := content.DojoStage(floor); stage > 0 {
		packets = append(packets,
			protocol.FieldScreenEffect("dojang/start/stage"),
			protocol.FieldScreenEffect("dojang/start/number/"+strconv.Itoa(stage)),
			protocol.FieldSoundEffect("Dojang/start"))
	}
	if !content.DojoRestFloor(floor) { // rest floors run no dojang_Eff and show no clock
		packets = append(packets, protocol.Clock(max(left, 0)))
	}
	packets = append(packets, protocol.SessionValue("energy", strconv.Itoa(energy)))
	if content.DojangField(c.MapID) {
		packets = append(packets, protocol.MapValue("Bamboo_Used", dojoFlag(bamboo)))
	}
	for _, p := range packets {
		_ = sink.WritePacket(p)
	}
}

func dojoFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// dojoPortal serves the floor portals: out00 (dojang_up) leaves for the exit
// room, out001 (dojang_next, and the out01 link at the same spot) climbs to
// the next floor once the floor is cleared.
func (s *Server) dojoPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	floor, _, ok := content.DojoFloorOf(actor.Character.MapID)
	next := p.Script == "dojang_next" || p.Name == "out01" || p.Name == "out001"
	if !ok || !next && p.Script != "dojang_up" {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.dojo == nil || inst.channel != channel || !inst.definition.Stage(actor.Character.MapID) {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if !next {
		s.cancelNPCScriptState(live, false)
		go s.eventExit(inst.channel, inst.definition, live, sink)
		return true, nil
	}
	run := inst.dojo
	run.mu.Lock()
	if run.floor != floor || !run.cleared {
		run.mu.Unlock()
		return true, eventPortalNotice(sink, "請先打倒這一層的對手。")
	}
	if floor == content.DojoFloors {
		run.mu.Unlock()
		s.broadcastInventoryPacket(channel, actor.Character.MapID, protocol.PortalTrainingMessage("恭喜你們通過了武陵道場的所有樓層！"))
		s.eventEnd(inst)
		return true, nil
	}
	from := actor.Character.MapID
	to := inst.definition.Maps[floor]
	field, err := s.gameplay.catalog.Map(to)
	if err != nil {
		run.mu.Unlock()
		return true, err
	}
	entry, ok := field.Spawn()
	if !ok {
		run.mu.Unlock()
		return true, domain.ErrInvalidInput
	}
	now := time.Now()
	before := struct {
		floorStart      time.Time
		elapsed         int
		cleared, bamboo bool
	}{run.floorStart, run.elapsed, run.cleared, run.bamboo}
	run.elapsed += int(now.Sub(run.floorStart) / time.Second)
	run.floor, run.floorStart = floor+1, now
	run.cleared, run.bamboo = content.DojoRestFloor(floor+1), false
	run.mu.Unlock()
	s.events.mu.Lock()
	previousDeadline := inst.deadline
	inst.deadline = dojoFloorDeadline(now, floor+1)
	s.events.mu.Unlock()
	err = s.moveEventRoom(ctx, inst, from, eventOccupant{live, sink}, to, entry)
	if err != nil && partySession(live).Character.MapID == from && len(s.activeEventMembers(inst, to)) == 0 {
		// Nobody arrived: the group stays on the floor it has cleared.
		run.mu.Lock()
		reverted := run.floor == floor+1
		if reverted {
			run.floor, run.floorStart, run.elapsed = floor, before.floorStart, before.elapsed
			run.cleared, run.bamboo = before.cleared, before.bamboo
		}
		run.mu.Unlock()
		if reverted {
			s.events.mu.Lock()
			inst.deadline = previousDeadline
			s.events.mu.Unlock()
		}
	}
	return true, err
}

// dojoMobDefeated clears a floor when its boss dies: the clear effect, and
// the door above the hidden next-floor portal opens.
func (s *Server) dojoMobDefeated(key mapKey, mobID int) {
	floor, _, ok := content.DojoFloorOf(key.mapID)
	boss, hasBoss := content.DojoBoss(floor)
	if !ok || !hasBoss || mobID != boss {
		return
	}
	inst := s.dojoInstance(key.channel, key.mapID)
	if inst == nil {
		return
	}
	run := inst.dojo
	run.mu.Lock()
	if run.floor != floor || run.cleared {
		run.mu.Unlock()
		return
	}
	run.cleared = true
	run.mu.Unlock()
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.FieldScreenEffect("dojang/end/clear"))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.FieldSoundEffect("Dojang/clear"))
	g := s.gameplay
	g.mu.Lock()
	var packets []roomDelivery
	if room := g.rooms[key]; room != nil {
		for _, door := range room.reactors {
			if door.def.TemplateID == content.DojoDoorReactor && door.state == 0 {
				door.state = 1
				packets = append(packets, roomBroadcast(room, protocol.ReactorState(door.objectID, 1, door.def.X, door.def.Y), -1)...)
			}
		}
	}
	g.mu.Unlock()
	deliver(packets)
}

// dojoAttackSettled fills the attacker's energy gauge: content.DojoEnergyPerHit
// for every committed attack that damaged a monster on a Dojang floor. The
// energy skills themselves do not refill it.
func (s *Server) dojoAttackSettled(t *combatTicket) {
	if t.monsterContact != nil || !content.DojangField(t.key.mapID) || skills.DojoSkill(t.request.SkillID) {
		return
	}
	damaged := 0
	for _, q := range t.request.Targets {
		if q.Damage > 0 {
			damaged++
		}
	}
	if damaged == 0 {
		return
	}
	id := t.actorSession.CharacterID
	inst := s.dojoInstance(t.key.channel, t.key.mapID)
	if inst == nil || s.eventOf(id) != inst {
		return
	}
	energy, changed := inst.dojo.gainEnergy(id, damaged*content.DojoEnergyPerHit)
	if changed {
		_ = t.sink.WritePacket(protocol.SessionValue("energy", strconv.Itoa(energy)))
	}
}

func (run *dojoRun) gainEnergy(id int64, amount int) (int, bool) {
	run.mu.Lock()
	defer run.mu.Unlock()
	before := run.energy[id]
	after := min(content.DojoEnergyFull, max(0, before+amount))
	run.energy[id] = after
	return after, after != before
}

// dojoSkillRun is the challenge in which c may use a dojo energy skill now:
// a member standing on the current floor of its run, which must be a Dojang
// floor, with a full gauge (and for 竹竿千擊 the floor's use still unspent).
// The caller holds c's persistMu, so one character's casts cannot race.
func (s *Server) dojoSkillRun(c domain.Character, skillID int) *dojoRun {
	floor, _, ok := content.DojoFloorOf(c.MapID)
	if !ok || !content.DojangField(c.MapID) || !skills.DojoSkill(skillID) || skillID/10000 != domain.BeginnerBook(c.Job) {
		return nil
	}
	inst := s.eventOf(c.ID)
	if inst == nil || inst.dojo == nil || !inst.definition.Stage(c.MapID) {
		return nil
	}
	run := inst.dojo
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.floor != floor || run.energy[c.ID] < content.DojoEnergyFull || skillID%10000 == 1009 && run.bamboo {
		return nil
	}
	return run
}

// spend empties c's gauge after an accepted energy skill; 竹竿千擊 also uses
// up the floor's one Bamboo Rain. It reports false when the gauge was taken
// meanwhile.
func (run *dojoRun) spend(id int64, skillID int) bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	bamboo := skillID%10000 == 1009
	if run.energy[id] < content.DojoEnergyFull || bamboo && run.bamboo {
		return false
	}
	run.energy[id] = 0
	if bamboo {
		run.bamboo = true
	}
	return true
}

// dojoSpendSkill spends the energy of an accepted cast or attack and tells
// the client; for 竹竿千擊 every member on the floor learns the floor's use.
func (s *Server) dojoSpendSkill(channel int, c domain.Character, sink packetSink, run *dojoRun, skillID int) bool {
	if run == nil || !run.spend(c.ID, skillID) {
		return false
	}
	_ = sink.WritePacket(protocol.SessionValue("energy", "0"))
	if skillID%10000 == 1009 {
		s.broadcastInventoryPacket(channel, c.MapID, protocol.MapValue("Bamboo_Used", "1"))
	}
	return true
}
