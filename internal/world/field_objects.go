package world

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// roomArea is an affected area (mist/smoke/aura) in one room.
type roomArea struct {
	objectID            int32
	ownerID             int64
	ownerSession        string
	ownerGeneration     int64
	ownerEntry          *gameplayMember
	reservation         *combatTicket // A pending Mist Eruption owns this exact area until its receipt resolves.
	skillID, level      int
	lt, rb              combat.Point
	x, y                int
	expiresAt, nextTick time.Time
	spec                *combat.FieldSpec
	effect              combat.Effect
	maxBase             int64
}

func (a *roomArea) packet() []byte {
	return protocol.AffectedAreaCreated(protocol.AffectedArea{ObjectID: a.objectID, Type: a.spec.AreaType, OwnerID: a.ownerID, SkillID: a.skillID, SkillLevel: a.level,
		Box: protocol.Rect{Left: int32(a.lt.X), Top: int32(a.lt.Y), Right: int32(a.rb.X), Bottom: int32(a.rb.Y)}, X: int16(a.x), Y: int16(a.y)})
}

// townPortal is one Mystic Door pair, keyed by channel and owner.
type townPortal struct {
	ownerID           int64
	ownerSession      string
	ownerGeneration   int64
	partyID           int64
	partySlot         int
	skillID           int
	fieldMap, townMap int
	fieldX, fieldY    int
	town              content.Portal
	expiresAt         time.Time
}

func (d *townPortal) partyRecord() domain.PartyTownPortal {
	return domain.PartyTownPortal{
		TownMap: d.townMap, FieldMap: d.fieldMap, SkillID: d.skillID,
		X: int16(d.fieldX), Y: int16(d.fieldY),
	}
}

// Called with partySpace.mu held. Field-object paths release gameplay.mu before
// publishing party updates, so a full snapshot never observes a stale cached door.
func (g *gameplaySpace) partyTownPortal(owner domain.CharacterSession, partyID int64, slot int) domain.PartyTownPortal {
	if g == nil {
		return domain.PartyTownPortal{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.doors[doorKey{owner.ChannelID, owner.CharacterID}]
	if d == nil || d.partyID != partyID || d.partySlot != slot || d.ownerSession != owner.SessionID ||
		d.ownerGeneration != owner.Generation || !time.Now().Before(d.expiresAt) {
		return domain.PartyTownPortal{}
	}
	return d.partyRecord()
}

type doorKey struct {
	channel int
	owner   int64
}

func fieldSkillEffect(skillID, level int, spec *combat.AttackSpec) (combat.Effect, time.Duration, error) {
	e := spec.Effect
	if e.SkillID == 0 {
		template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID)
		if err != nil {
			return e, 0, err
		}
		if e, err = combat.FromSkill(template, level); err != nil {
			return e, 0, err
		}
	}
	d := spec.Field.Duration
	if d == 0 {
		d = e.Time
	}
	if d <= 0 || d > 30*time.Minute {
		return e, 0, fmt.Errorf("skill%d field object has no duration", skillID)
	}
	return e, d, nil
}

// SpawnFieldObject places the registered field object for a successful cast
// at the caster's position (S0a's cast path calls it, like SpawnSummon).
func (s *Server) SpawnFieldObject(channel int, live *liveCharacter, skillID, level int, now time.Time) (int32, error) {
	spec, ok := attackSpecFor(skillID)
	if !ok || spec.Field == nil || level < 1 || s.gameplay == nil {
		return 0, fmt.Errorf("skill%d has no registered field object", skillID)
	}
	if spec.Field.Kind == combat.FieldTownPortal {
		return 0, s.spawnTownPortal(channel, live, skillID, level, spec, now)
	}
	if spec.Field.Kind == combat.FieldOpenGate {
		return 0, s.spawnOpenGate(channel, live, skillID, level, spec, now)
	}
	effect, d, err := fieldSkillEffect(skillID, level, spec)
	if err != nil {
		return 0, err
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	box := spec.Field.Box
	if !box.Set {
		box = effect.Box
	}
	if !box.Set {
		return 0, fmt.Errorf("skill%d area has no lt/rb", skillID)
	}
	stats, err := combat.AttackStats(c, now)
	if err != nil {
		return 0, err
	}
	lt, rb := box.At(combat.Point{X: c.X, Y: c.Y}, true)
	interval := spec.Field.Interval
	if interval <= 0 {
		interval = time.Second
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return 0, fmt.Errorf("area owner not in field")
	}
	oid := g.alloc()
	if oid == 0 {
		g.mu.Unlock()
		return 0, fmt.Errorf("object ID space exhausted")
	}
	a := &roomArea{objectID: oid, ownerID: c.ID, ownerSession: session.SessionID, ownerGeneration: session.Generation, ownerEntry: room.members[c.ID], skillID: skillID, level: level, lt: lt, rb: rb, x: c.X, y: c.Y,
		expiresAt: now.Add(d), nextTick: now, spec: spec.Field, effect: effect, maxBase: combat.MaxBaseDamage(stats)}
	if room.areas == nil {
		room.areas = map[int32]*roomArea{}
	}
	room.areas[oid] = a
	out := roomBroadcast(room, a.packet(), -1)
	g.mu.Unlock()
	deliver(out)
	return oid, nil
}

func areaEnterPacketsLocked(room *gameplayRoom) [][]byte {
	var out [][]byte
	for _, a := range room.areas {
		out = append(out, a.packet())
	}
	return out
}

// tickAreas expires areas and applies their monster statuses each interval.
func (g *gameplaySpace) tickAreas(now time.Time) {
	var out []roomDelivery
	var recover []areaRecovery
	g.mu.Lock()
	for key, room := range g.rooms {
		for id, a := range room.areas {
			if a.reservation != nil {
				continue // An unknown combat receipt must not lose or reuse its selected areas.
			}
			if !now.Before(a.expiresAt) {
				delete(room.areas, id)
				out = append(out, roomBroadcast(room, protocol.AffectedAreaRemoved(a.objectID, a.skillID, false), -1)...)
				continue
			}
			if len(a.spec.MobStatus) == 0 && !a.spec.RecoverMP || now.Before(a.nextTick) {
				continue
			}
			a.nextTick = now.Add(max(a.spec.Interval, time.Second))
			if a.spec.RecoverMP && a.effect.X > 0 {
				for _, m := range room.members {
					if a.effect.Prop <= 0 || rollProp(a.effect.Prop) {
						recover = append(recover, areaRecovery{key: key, member: m, lt: a.lt, rb: a.rb, percent: a.effect.X})
					}
				}
			}
			if len(a.spec.MobStatus) == 0 {
				continue
			}
			owner := room.members[a.ownerID]
			if owner == nil || owner.sessionID != a.ownerSession || owner.generation != a.ownerGeneration {
				continue // Java stops poison credit once the owner left the field.
			}
			session := domain.CharacterSession{CharacterID: a.ownerID, SessionID: a.ownerSession, Generation: a.ownerGeneration}
			for _, mob := range room.mobs {
				if mob.hp <= 0 || mob.claiming || !combat.Inside(a.lt, a.rb, combat.Point{X: mob.life.X, Y: mob.life.Y}) {
					continue
				}
				var add []combat.ActiveStatus
				for _, ap := range a.spec.MobStatus {
					if mob.statuses.Has(ap.Stat) || !combat.CanInflict(mob.stats, ap.Stat, a.effect.Element) {
						continue
					}
					if st, ok := combat.ResolveOn(ap, a.effect, a.ownerID, a.maxBase, mob.stats, now, rollProp); ok {
						add = append(add, st)
					}
				}
				if p := applyMobStatusesLocked(mob, add, session, now); p != nil {
					out = append(out, roomBroadcast(room, p, -1)...)
				}
			}
		}
	}
	g.mu.Unlock()
	deliver(out)
	for _, r := range recover {
		r.apply()
	}
}

// areaRecovery is one pending Recovery Aura tick, applied outside g.mu.
type areaRecovery struct {
	key     mapKey
	member  *gameplayMember
	lt, rb  combat.Point
	percent int
}

// apply restores transient MP (like regeneration) when the member is still
// the same live visit, alive and inside the area.
func (r areaRecovery) apply() {
	live := r.member.live
	if live == nil {
		return
	}
	live.mu.Lock()
	c := &live.session.Character
	if live.session.SessionID != r.member.sessionID || live.session.Generation != r.member.generation || live.session.ChannelID != r.key.channel || c.MapID != r.key.mapID ||
		c.HP <= 0 || c.MP >= c.MaxMP || !combat.Inside(r.lt, r.rb, combat.Point{X: c.X, Y: c.Y}) {
		live.mu.Unlock()
		return
	}
	c.MP = min(c.MaxMP, c.MP+c.MaxMP*r.percent/100) // Java addMP(x * maxMp / 100).
	live.dirty = true
	hp, mp := c.HP, c.MP
	live.mu.Unlock()
	_ = r.member.sink.WritePacket(protocol.UpdateHPMP(hp, mp))
}

// PlayerInArea reports whether a point lies in an area of the given type
// owned by one of owners (for Smoke Screen style damage immunity).
func (s *Server) PlayerInArea(channel, mapID, x, y int, owners map[int64]bool) bool {
	if s.gameplay == nil {
		return false
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel: channel, mapID: mapID}]
	if room == nil {
		return false
	}
	for _, a := range room.areas {
		if a.spec.PlayersInvincible && owners[a.ownerID] && combat.Inside(a.lt, a.rb, combat.Point{X: x, Y: y}) {
			return true
		}
	}
	return false
}

// partyOf is the runtime party ID (0 = none) without touching gameplay.mu.
func (s *Server) partyOf(id int64) int64 {
	if s.parties == nil {
		return 0
	}
	s.parties.mu.Lock()
	defer s.parties.mu.Unlock()
	if p := s.parties.peers[id]; p != nil {
		return p.party.ID
	}
	return 0
}

func (d *townPortal) visibleTo(id, party int64) bool {
	return id == d.ownerID || d.partyID != 0 && party == d.partyID
}

func (s *Server) spawnTownPortal(channel int, live *liveCharacter, skillID, level int, spec *combat.AttackSpec, now time.Time) error {
	_, d, err := fieldSkillEffect(skillID, level, spec)
	if err != nil {
		return err
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	g := s.gameplay
	field, err := g.catalog.Map(c.MapID)
	if err != nil || field.Town || field.ReturnMap <= 0 || field.ReturnMap == c.MapID || field.ReturnMap == protocol.NoTownPortal {
		return fmt.Errorf("no town for a door from map%d", c.MapID)
	}
	town, err := g.catalog.Map(field.ReturnMap)
	if err != nil {
		return err
	}
	var party int64
	var slot int
	members := map[int64]int64{}
	if s.parties != nil {
		s.parties.mu.Lock()
		if peer := s.parties.peers[c.ID]; peer != nil && peer.live == live {
			if owner, ok := peer.party.Member(c.ID); ok {
				party, slot = peer.party.ID, owner.Slot
				for id, m := range s.parties.peers {
					members[id] = m.party.ID
				}
			}
		}
		s.parties.mu.Unlock()
	}
	exit, found := townPortalExit(town, slot)
	if !found {
		return fmt.Errorf("town%d has no door portal for slot%d", town.ID, slot)
	}
	door := &townPortal{ownerID: c.ID, ownerSession: session.SessionID, ownerGeneration: session.Generation, partyID: party, partySlot: slot, skillID: skillID, fieldMap: c.MapID, townMap: town.ID, fieldX: c.X, fieldY: c.Y, town: exit, expiresAt: now.Add(d)}
	g.mu.Lock()
	fieldRoom := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if fieldRoom == nil || fieldRoom.members[c.ID] == nil || fieldRoom.members[c.ID].live != live {
		g.mu.Unlock()
		return fmt.Errorf("door owner not in field")
	}
	if g.doors == nil {
		g.doors = map[doorKey]*townPortal{}
	}
	key := doorKey{channel, c.ID}
	var out []roomDelivery
	if old := g.doors[key]; old != nil {
		out = append(out, g.doorPacketsLocked(channel, old, members, false)...)
	}
	g.doors[key] = door
	out = append(out, g.doorPacketsLocked(channel, door, members, true)...)
	g.mu.Unlock()
	deliver(out)
	s.publishPartyTownPortal(door, channel)
	return nil
}

// CField stores the town's type-6 portals in WZ order, then indexes that
// list with the party member slot (0x503FD4). Solo use always checks slot zero.
func townPortalExit(town content.Map, slot int) (content.Portal, bool) {
	for _, portal := range town.Portals {
		if portal.Type != doorEntryType {
			continue
		}
		if slot == 0 {
			return portal, true
		}
		slot--
	}
	return content.Portal{}, false
}

// doorPacketsLocked creates (or removes) both portal halves for the owner and
// party members in either room, plus the town-portal info for each of them.
func (g *gameplaySpace) doorPacketsLocked(channel int, d *townPortal, members map[int64]int64, create bool) []roomDelivery {
	var out []roomDelivery
	for _, half := range []struct {
		mapID int
		x, y  int
	}{{d.fieldMap, d.fieldX, d.fieldY}, {d.townMap, d.town.X, d.town.Y}} {
		room := g.rooms[mapKey{channel: channel, mapID: half.mapID}]
		if room == nil {
			continue
		}
		info := protocol.TownPortalInfo(protocol.NoTownPortal, protocol.NoTownPortal, 0, 0, 0)
		p := protocol.TownPortalRemoved(d.ownerID, true)
		if create {
			p = protocol.TownPortalCreated(d.ownerID, int16(half.x), int16(half.y), true)
			info = protocol.TownPortalInfo(d.townMap, d.fieldMap, d.skillID, int16(d.fieldX), int16(d.fieldY))
		}
		for id, m := range room.members {
			if d.visibleTo(id, members[id]) {
				out = append(out, roomDelivery{m.sink, p}, roomDelivery{m.sink, info})
			}
		}
	}
	return out
}

// doorEnterPacketsLocked returns door halves visible to a joining member.
func (g *gameplaySpace) doorEnterPacketsLocked(channel, mapID int, id, party int64) [][]byte {
	var out [][]byte
	for k, d := range g.doors {
		if k.channel != channel || !d.visibleTo(id, party) {
			continue
		}
		switch mapID {
		case d.fieldMap:
			out = append(out, protocol.TownPortalCreated(d.ownerID, int16(d.fieldX), int16(d.fieldY), false), protocol.TownPortalInfo(d.townMap, d.fieldMap, d.skillID, int16(d.fieldX), int16(d.fieldY)))
		case d.townMap:
			out = append(out, protocol.TownPortalCreated(d.ownerID, int16(d.town.X), int16(d.town.Y), false), protocol.TownPortalInfo(d.townMap, d.fieldMap, d.skillID, int16(d.fieldX), int16(d.fieldY)))
		}
	}
	return out
}

func (s *Server) tickDoors(now time.Time) {
	g := s.gameplay
	members := s.partySnapshot()
	var out []roomDelivery
	var expired map[doorKey]*townPortal
	g.mu.Lock()
	for k, d := range g.doors {
		if !now.Before(d.expiresAt) {
			delete(g.doors, k)
			if expired == nil {
				expired = make(map[doorKey]*townPortal)
			}
			expired[k] = d
			out = append(out, g.doorPacketsLocked(k.channel, d, members, false)...)
		}
	}
	g.mu.Unlock()
	deliver(out)
	for k, d := range expired {
		s.publishPartyTownPortal(d, k.channel)
	}
}

// RemoveTownPortal drops an owner's door (logout, channel change, recast).
func (s *Server) RemoveTownPortal(channel int, ownerID int64) {
	if s.gameplay == nil {
		return
	}
	members := s.partySnapshot()
	g := s.gameplay
	g.mu.Lock()
	var out []roomDelivery
	d := g.doors[doorKey{channel, ownerID}]
	if d != nil {
		delete(g.doors, doorKey{channel, ownerID})
		out = g.doorPacketsLocked(channel, d, members, false)
	}
	g.mu.Unlock()
	deliver(out)
	if d != nil {
		s.publishPartyTownPortal(d, channel)
	}
}

func (s *Server) publishPartyTownPortal(d *townPortal, channel int) {
	if d.partyID == 0 || s.parties == nil {
		return
	}
	b := s.parties
	b.mu.Lock()
	defer b.mu.Unlock()
	owner := b.peers[d.ownerID]
	if owner == nil || owner.party.ID != d.partyID {
		return
	}
	session := partySession(owner.live)
	if session.ChannelID != channel {
		return
	}
	current := s.gameplay.partyTownPortal(session, d.partyID, d.partySlot)
	packet := protocol.PartyTownPortalUpdate(d.partySlot, current)
	var out []partyDelivery
	for _, member := range owner.party.Members {
		recipient := b.peers[member.CharacterID]
		if recipient != nil && recipient.party.ID == d.partyID && partySession(recipient.live).ChannelID == channel {
			out = append(out, partyDelivery{peer: recipient, packets: [][]byte{packet}})
		}
	}
	queueParty(out)
}

func (s *Server) partySnapshot() map[int64]int64 {
	members := map[int64]int64{}
	if s.parties == nil {
		return members
	}
	s.parties.mu.Lock()
	for id, p := range s.parties.peers {
		members[id] = p.party.ID
	}
	s.parties.mu.Unlock()
	return members
}

// useTownPortal warps through a visible door: field side to the town exit,
// town side back to the door position in the field.
func (s *Server) useTownPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, u protocol.UseTownPortal) error {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	party := s.partyOf(c.ID)
	g := s.gameplay
	g.mu.Lock()
	d := g.doors[doorKey{channel, u.OwnerID}]
	var target int
	var entry content.Portal
	if d != nil && d.visibleTo(c.ID, party) && time.Now().Before(d.expiresAt) {
		switch {
		case c.MapID == d.fieldMap && u.ToTown && nearTownPortal(c, d.fieldX, d.fieldY):
			target, entry = d.townMap, d.town
		case c.MapID == d.townMap && !u.ToTown && nearTownPortal(c, d.town.X, d.town.Y):
			target, entry = d.fieldMap, content.Portal{X: d.fieldX, Y: d.fieldY}
		}
	}
	g.mu.Unlock()
	if target == 0 {
		slog.Warn("town_portal_use_rejected", "character", c.ID, "owner", u.OwnerID, "to_town", u.ToTown, "map", c.MapID, "door", d != nil, "visible", d != nil && d.visibleTo(c.ID, party), "door_field", doorMap(d, true), "door_town", doorMap(d, false))
		return sink.WritePacket(protocol.EnableActions())
	}
	if entry.ID == 0 && target == d.fieldMap {
		if field, err := g.catalog.Map(target); err == nil {
			best := -1
			for _, p := range field.Portals {
				dist := (p.X-d.fieldX)*(p.X-d.fieldX) + (p.Y-d.fieldY)*(p.Y-d.fieldY)
				if best < 0 || dist < best {
					best, entry.ID = dist, p.ID
				}
			}
		}
		entry.X, entry.Y = d.fieldX, d.fieldY
	}
	entry.Type = doorEntryType
	slog.Info("town_portal_used", "character", c.ID, "owner", u.OwnerID, "to_town", u.ToTown, "target", target, "x", entry.X, "y", entry.Y)
	if err := s.warpGameplay(ctx, channel, live, sink, target, entry); err != nil {
		return err
	}
	// USE_DOOR set the client's exclusive-request flag (SetExclRequestSent,
	// 0x4F3154); the return door is ignored until enableActions clears it.
	return sink.WritePacket(protocol.EnableActions())
}

// Both original E7 senders use PtInRect at ±20 x and ±50 y.
func nearTownPortal(c domain.Character, x, y int) bool {
	return c.X >= x-20 && c.X < x+20 && c.Y >= y-50 && c.Y < y+50
}

// doorEntryType marks a warp entry as a Mystic Door exit (the WZ tp portal
// type): the user is placed exactly at entry.X/Y.
const doorEntryType = 6

func doorMap(d *townPortal, field bool) int {
	switch {
	case d == nil:
		return 0
	case field:
		return d.fieldMap
	default:
		return d.townMap
	}
}
