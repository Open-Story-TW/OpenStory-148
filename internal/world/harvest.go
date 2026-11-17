package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// roomReactor is one herb patch or ore vein of a map. A gatherer claims it,
// hits it through its states, and the last hit removes it until it regrows.
type roomReactor struct {
	objectID  int32
	def       content.Reactor
	node      content.GatherNode
	state     int
	gone      bool
	respawnAt time.Time
	harvester int64
	finishing bool
	lastHit   time.Time
	drainsAt  time.Time
	growth    int
	mineHits  int
}

// gatherLastState is the state a node reaches on the last hit: the WZ
// templates have states 0-4 and one hit event from each state to the next.
const gatherLastState = 4

// gatherReach is how far from a node a gatherer may stand. The client only
// offers a node it overlaps (60 by 30 pixels around the node's own bounds).
const gatherReachX, gatherReachY = 250, 200

// gatherDropOffset is how far to the side of a node its yield lands.
const gatherDropOffset = 45

func (g *gameplaySpace) addReactorsLocked(r *gameplayRoom, mapID int) {
	reactors, err := content.MapReactors(wzRoot(), mapID)
	if err != nil || len(reactors) == 0 {
		return
	}
	r.reactors = make(map[int32]*roomReactor, len(reactors))
	for _, def := range reactors {
		var node content.GatherNode
		var mineHits int
		if content.ZakumMineMap(mapID) && content.ZakumMineReactor(def.TemplateID) {
			mineHits, err = content.ZakumMineHitCount(wzRoot(), def.TemplateID)
			if err != nil {
				continue
			}
		} else if def.TemplateID != content.HeliosElevatorDoor && def.TemplateID != content.KrexelReactor &&
			!content.TreasureHuntReactor(mapID, def.TemplateID) && !content.EscapeReactor(mapID, def.TemplateID) &&
			!content.RomeoLabReactor(mapID, def.TemplateID) {
			node, err = content.GatherNodeDefinition(wzRoot(), def.TemplateID)
			if err != nil {
				continue
			}
		}
		oid := g.alloc()
		if oid == 0 {
			return
		}
		r.reactors[oid] = &roomReactor{objectID: oid, def: def, node: node, mineHits: mineHits}
	}
}

func (x *roomReactor) enterPacket() []byte {
	return protocol.ReactorEnter(x.objectID, x.def.TemplateID, x.state, x.def.X, x.def.Y, x.def.Facing)
}

// regrowLocked brings back a node whose time has come.
func (x *roomReactor) regrowLocked(now time.Time) bool {
	if !x.gone || x.respawnAt.IsZero() || now.Before(x.respawnAt) {
		return false
	}
	x.gone, x.state, x.harvester = false, 0, 0
	x.growth++
	return true
}

// reactorEnterPacketsLocked shows a newcomer the nodes that are standing.
func reactorEnterPacketsLocked(r *gameplayRoom, now time.Time) [][]byte {
	var out [][]byte
	for _, x := range r.reactors {
		x.regrowLocked(now)
		if !x.gone {
			out = append(out, x.enterPacket())
		}
	}
	return out
}

// releaseHarvestLocked frees the nodes a departing or interrupted gatherer
// held and returns the packets that put them back to their first state.
func releaseHarvestLocked(r *gameplayRoom, characterID int64) [][]byte {
	var out [][]byte
	for _, x := range r.reactors {
		if x.harvester != characterID {
			continue
		}
		x.harvester = 0
		if !x.gone && x.state != 0 {
			x.state = 0
			out = append(out, protocol.ReactorState(x.objectID, 0, x.def.X, x.def.Y))
		}
	}
	return out
}

func (g *gameplaySpace) tickReactors(now time.Time) {
	type delivery struct {
		sink   packetSink
		packet []byte
	}
	var packets []delivery
	g.mu.Lock()
	for _, r := range g.rooms {
		if len(r.members) == 0 {
			continue
		}
		for _, x := range r.reactors {
			var p []byte
			if x.drainRomeoBeaker(now) {
				p = protocol.ReactorState(x.objectID, x.state, x.def.X, x.def.Y)
			} else if x.regrowLocked(now) {
				p = x.enterPacket()
			} else {
				continue
			}
			for _, m := range r.members {
				packets = append(packets, delivery{m.sink, p})
			}
		}
	}
	g.mu.Unlock()
	for _, d := range packets {
		_ = d.sink.WritePacket(d.packet)
	}
}

// gatherTool finds a gathering tool in the equipment tab. Like the client
// (0x69039B) it looks at carried tools, not worn ones.
func gatherTool(c domain.Character, profession int) int {
	family := content.GatherTool(profession)
	for _, e := range c.Equipment {
		if e.Slot > 0 && e.ItemID/10000 == family {
			return e.ItemID
		}
	}
	return 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// startHarvest answers a start-harvest request: the client waits for the
// start code before it plays the gathering animation and sends its hits.
func (s *Server) startHarvest(channel int, live *liveCharacter, sink packetSink, objectID int32) error {
	fatigue := s.restFatigue(live, sink)
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	code, tool := protocol.HarvestUnknown, 0
	var others []packetSink
	var released [][]byte
	var all []packetSink
	g := s.gameplay
	g.mu.Lock()
	if r := g.rooms[mapKey{channel: channel, mapID: c.MapID}]; r != nil {
		if x := r.reactors[objectID]; x != nil && x.node.Profession != 0 && !x.gone && c.HP > 0 {
			level := c.Profession(x.node.Profession).Level
			tool = gatherTool(c, x.node.Profession)
			switch {
			case x.harvester != 0 && x.harvester != c.ID:
				code = protocol.HarvestBusy
			case level == 0 && x.node.Profession == content.ProfessionHerbalism:
				code = protocol.HarvestNoHerbs
			case level == 0:
				code = protocol.HarvestNoMining
			case level < x.node.Level:
				code = protocol.HarvestLowLevel
			case tool == 0:
				code = protocol.HarvestNoTool
			case !x.node.Free && fatigue >= content.ProfessionFatigueLimit:
				code = protocol.HarvestTired
			case abs(c.X-x.def.X) > gatherReachX || abs(c.Y-x.def.Y) > gatherReachY:
				code = protocol.HarvestTooFar
			default:
				code = protocol.HarvestStart
				released = releaseHarvestLocked(r, c.ID)
				x.harvester, x.state = c.ID, 0
			}
		}
		for id, m := range r.members {
			all = append(all, m.sink)
			if id != c.ID {
				others = append(others, m.sink)
			}
		}
	}
	g.mu.Unlock()
	for _, p := range released {
		for _, peer := range all {
			_ = peer.WritePacket(p)
		}
	}
	if code == protocol.HarvestStart {
		for _, peer := range others {
			_ = peer.WritePacket(protocol.ShowHarvesting(c.ID, tool))
		}
	}
	return sink.WritePacket(protocol.HarvestMessage(objectID, code))
}

// hitReactor advances the node the character is gathering. Hits on anything
// else are ignored: gathering nodes are the only reactors the server runs.
func (s *Server) hitReactor(ctx context.Context, channel int, live *liveCharacter, sink packetSink, objectID int32, stop bool) error {
	if !stop {
		if handled, err := s.hitZakumMineReactor(ctx, channel, live, sink, objectID); handled {
			return err
		}
		if handled, err := s.hitTreasureChest(ctx, channel, live, sink, objectID); handled {
			return err
		}
	}
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	if !stop && s.hitHorntailCrystal(channel, c, objectID) {
		return sink.WritePacket(protocol.EnableActions())
	}
	var all, others []packetSink
	var packets [][]byte
	var node content.GatherNode
	var place content.Reactor
	finished, held := false, false
	g := s.gameplay
	g.mu.Lock()
	if r := g.rooms[mapKey{channel: channel, mapID: c.MapID}]; r != nil {
		if x := r.reactors[objectID]; x != nil && !x.gone && x.harvester == c.ID {
			held = true
			if !stop {
				x.state++
			}
			switch {
			case x.state >= gatherLastState || stop && x.state >= gatherLastState-1:
				// The last hit, or a stop the client sends in its place.
				finished, node, place = true, x.node, x.def
				seconds := x.def.Respawn
				if seconds <= 0 {
					seconds = x.node.Respawn
				}
				x.gone, x.harvester, x.respawnAt = true, 0, time.Now().Add(time.Duration(max(seconds, 3))*time.Second)
				packets = append(packets, protocol.ReactorLeave(x.objectID, gatherLastState, x.def.X, x.def.Y))
			case stop:
				packets = releaseHarvestLocked(r, c.ID)
			default:
				packets = append(packets, protocol.ReactorState(x.objectID, x.state, x.def.X, x.def.Y))
			}
		}
		for id, m := range r.members {
			all = append(all, m.sink)
			if id != c.ID {
				others = append(others, m.sink)
			}
		}
	}
	g.mu.Unlock()
	if !held {
		return nil
	}
	for _, p := range packets {
		for _, peer := range all {
			_ = peer.WritePacket(p)
		}
	}
	if !finished {
		if stop {
			for _, peer := range others {
				_ = peer.WritePacket(protocol.ShowHarvesting(c.ID, 0))
			}
		}
		return nil
	}
	success, err := s.finishHarvest(ctx, live, sink, node, place)
	for _, peer := range others {
		_ = peer.WritePacket(protocol.ShowHarvesting(c.ID, 0))
		_ = peer.WritePacket(protocol.Harvested(c.ID, success))
	}
	if err != nil {
		return err
	}
	return sink.WritePacket(protocol.Harvested(c.ID, success))
}

// finishHarvest decides what a finished node gives and commits it: the yield
// falls to the ground where the node stood, for the gatherer to pick up, in
// the same commit as the proficiency, fatigue and trait.
//
// Odds and gains follow Legacy's doHarvest: 90% at a node of the gatherer's
// own level, 10% more or less per level of difference; a success is worth
// 20 proficiency (2 more or less per level), a failure a tenth of that and a
// root or ore fragment instead of the yield.
func (s *Server) finishHarvest(ctx context.Context, live *liveCharacter, sink packetSink, node content.GatherNode, place content.Reactor) (bool, error) {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	name, scrap := "藥草採集", content.HerbRoots
	if node.Profession == content.ProfessionMining {
		name, scrap = "採礦", content.OreFragments
	}
	level := c.Profession(node.Profession).Level
	rank := node.TemplateID % 100
	chance := 90 + (level-rank)*10
	switch rank {
	case 10: // practice node
		rank, chance = 1, 100
	case 11: // rare node
		rank, chance = 10, chance-40
	}
	gain := max((rank-level)*2+20, 1)
	success := rand.IntN(100) < chance
	yield, err := content.GatherYield(wzRoot(), node)
	if err != nil {
		success = false
	}
	change := domain.ProfessionChange{}
	if !node.Free {
		change.Fatigue = 1
	}
	// The yield lands beside the node, on the gatherer's side: the node grows
	// back within seconds and would cover a drop left exactly on its spot.
	side := gatherDropOffset
	if c.X < place.X {
		side = -side
	}
	drop := domain.ScriptDrop{TemplateID: scrap, Quantity: 1}
	drop.X, drop.Y = s.gatherDropPoint(c.MapID, place.X, place.Y, side)
	if success {
		drop.TemplateID = yield[rand.IntN(len(yield))]
		if node.Profession == content.ProfessionHerbalism {
			change.Traits.Sense = 5
		} else {
			change.Traits.Insight = 5
		}
	} else {
		gain = max(gain/10, 1)
	}
	if level < content.ProfessionMaxLevel {
		change.Gain, change.EXP = node.Profession, gain
	}
	after, ok, err := s.commitProfession(ctx, live, sink, fmt.Sprintf("harvest-%d-%d-%d", c.ID, node.TemplateID, time.Now().UnixNano()), nil, change, drop)
	if err != nil || !ok {
		return false, err
	}
	if change.Gain != 0 {
		if err = sink.WritePacket(protocol.PortalTrainingMessage(fmt.Sprintf("%s的熟練度增加。 (+%d)", name, gain))); err != nil {
			return success, err
		}
		if after.Profession(node.Profession).Level > level {
			if err = sink.WritePacket(protocol.PortalTrainingMessage(name + "的等級提升了。")); err != nil {
				return success, err
			}
		}
	}
	return success, nil
}

// gatherDropPoint is where a node's yield lands: on the platform the node
// stands on, side pixels from it, or at the node itself where the platform
// ends before that. The point has to lie on a foothold: the client draws a
// drop in the layer of the foothold under it (0x4ED2DE looks one pixel around
// the point), and one that hangs above the ground, as a node's own position
// does by a few pixels, falls into the background layer behind the tiles.
func (s *Server) gatherDropPoint(mapID, x, y, side int) (int, int) {
	if s.gameplay == nil || s.gameplay.catalog == nil {
		return x, y
	}
	m, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return x, y
	}
	for _, at := range []int{x + side, x} {
		if ground, ok := groundBelow(m, at, y); ok && ground-y <= 40 {
			return at, ground
		}
	}
	return x, y
}

// groundBelow is the highest foothold at x that is not above y (with the
// slack a node standing a few pixels into the ground needs).
func groundBelow(m content.Map, x, y int) (int, bool) {
	best, found := 0, false
	for _, f := range m.Footholds {
		x1, y1, x2, y2 := f.X1, f.Y1, f.X2, f.Y2
		if x1 > x2 {
			x1, y1, x2, y2 = x2, y2, x1, y1
		}
		if x1 == x2 || x < x1 || x > x2 {
			continue
		}
		at := y1 + (y2-y1)*(x-x1)/(x2-x1)
		if at < y-10 {
			continue
		}
		if !found || at < best {
			best, found = at, true
		}
	}
	return best, found
}
