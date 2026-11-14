package world

import (
	"fmt"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"reflect"
	"sync"
	"sync/atomic"

	"maple-go-server/internal/protocol"
)

type mesoDrop struct {
	objectID int32
	amount   int32
	ownerID  int64
	x, y     int
	claimed  bool
	dropOp   string
	pickupOp string
}

type dropSpace struct {
	mu            sync.Mutex
	nextID        atomic.Int32
	opSeq         atomic.Uint64
	bootID        string
	byRoom        map[mapKey]map[int32]*mesoDrop
	sinks         map[mapKey]map[int64]packetSink // character sinks for broadcast
	questViews    map[mapKey]map[int64]map[int32]questDropDecision
	questTracks   map[mapKey]map[int64]*questDropTrack
	trackSequence uint64
	questRefresh  map[int64]*questRefreshWorker
}

type questDropDecision struct {
	visible  bool
	revision int64
	version  uint64
}
type questDropTrack struct {
	sink           packetSink
	session        domain.CharacterSession
	bound          bool
	fixtureBinding bool
	token, version uint64
}

func cloneDropSession(s domain.CharacterSession) domain.CharacterSession {
	s.Character.Items = append([]domain.InventoryItem(nil), s.Character.Items...)
	s.Character.Quests = append([]domain.QuestState(nil), s.Character.Quests...)
	for i := range s.Character.Quests {
		q := &s.Character.Quests[i]
		q.MobOrder = append([]int(nil), q.MobOrder...)
		if q.MobKills != nil {
			counts := make(map[int]int, len(q.MobKills))
			for id, n := range q.MobKills {
				counts[id] = n
			}
			q.MobKills = counts
		}
	}
	return s
}

func sameDropSink(a, b packetSink) bool {
	if a == nil || b == nil {
		return false
	}
	t := reflect.TypeOf(a)
	return t == reflect.TypeOf(b) && t.Comparable() && a == b
}

func newDropSpace() *dropSpace {
	d := &dropSpace{byRoom: map[mapKey]map[int32]*mesoDrop{}, sinks: map[mapKey]map[int64]packetSink{}, bootID: platform.NewID()}
	d.nextID.Store(1)
	return d
}

func (d *dropSpace) Track(channel, mapID int, characterID int64, sink packetSink, sessions ...domain.CharacterSession) {
	key := mapKey{channel: channel, mapID: mapID}
	d.mu.Lock()
	defer d.mu.Unlock()
	room := d.sinks[key]
	if room == nil {
		room = map[int64]packetSink{}
		d.sinks[key] = room
	}
	room[characterID] = sink
	if worker := d.questRefresh[characterID]; worker != nil {
		worker.pending = nil
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	if d.questTracks == nil {
		d.questTracks = map[mapKey]map[int64]*questDropTrack{}
	}
	if d.questTracks[key] == nil {
		d.questTracks[key] = map[int64]*questDropTrack{}
	}
	d.trackSequence++
	track := &questDropTrack{sink: sink, token: d.trackSequence, version: 1, fixtureBinding: len(sessions) == 0}
	if len(sessions) == 1 {
		session := sessions[0]
		if session.CharacterID == characterID && session.Character.ID == characterID && session.ChannelID == channel && session.Character.MapID == mapID {
			track.session = cloneDropSession(session)
			track.bound = true
		}
	}
	d.questTracks[key][characterID] = track
	if d.questViews != nil {
		if views := d.questViews[key]; views != nil {
			delete(views, characterID)
		}
	}
}

// Called with a detached authoritative live snapshot after publication. The
// monotonic version also distinguishes quest changes at an equal revision.
func (d *dropSpace) UpdateSession(session domain.CharacterSession) {
	d.updateSession(session)
}

func (d *dropSpace) updateSession(session domain.CharacterSession) (domain.Character, bool) {
	if d == nil {
		return domain.Character{}, false
	}
	key := mapKey{session.ChannelID, session.Character.MapID}
	d.mu.Lock()
	defer d.mu.Unlock()
	track := d.questTracks[key][session.CharacterID]
	if track == nil || !track.bound || track.session.SessionID != session.SessionID || track.session.Generation != session.Generation || session.Character.Revision < track.session.Character.Revision {
		return domain.Character{}, false
	}
	before := track.session.Character
	track.session = cloneDropSession(session)
	track.version++
	return before, true
}

func (d *dropSpace) Untrack(channel, mapID int, characterID int64, expected ...packetSink) {
	key := mapKey{channel: channel, mapID: mapID}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) > 0 && !sameDropSink(d.sinks[key][characterID], expected[0]) {
		return
	}
	if worker := d.questRefresh[characterID]; worker != nil {
		worker.pending = nil
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	if room := d.sinks[key]; room != nil {
		delete(room, characterID)
		if len(room) == 0 {
			delete(d.sinks, key)
		}
	}
	if views := d.questViews[key]; views != nil {
		delete(views, characterID)
		if len(views) == 0 {
			delete(d.questViews, key)
		}
	}
	if tracks := d.questTracks[key]; tracks != nil {
		delete(tracks, characterID)
		if len(tracks) == 0 {
			delete(d.questTracks, key)
		}
	}
}

func (d *dropSpace) AllocObjectID() int32 {
	for {
		next := d.nextID.Load()
		if next >= 1000000 {
			return 0
		}
		if d.nextID.CompareAndSwap(next, next+1) {
			return next
		}
	}
}

func (d *dropSpace) nextOperationID(kind string, characterID int64, objectID int32) string {
	seq := d.opSeq.Add(1)
	// operation_id must be >=16 chars and unique across restarts within a DB.
	return fmt.Sprintf("%s-%s-%d-%d-%d-op", kind, d.bootID, characterID, objectID, seq)
}

func (d *dropSpace) RegisterMeso(channel, mapID int, objectID, amount int32, ownerID int64, x, y int, dropOp, pickupOp string) []byte {
	if objectID <= 0 || amount < 10 || amount > 50000 || ownerID <= 0 || len(dropOp) < 16 || len(pickupOp) < 16 {
		return nil
	}
	key := mapKey{channel: channel, mapID: mapID}
	drop := &mesoDrop{objectID: objectID, amount: amount, ownerID: ownerID, x: x, y: y, dropOp: dropOp, pickupOp: pickupOp}
	packet := protocol.DropMesoOnMap(objectID, amount, int32(ownerID), int16(x), int16(y), true)
	d.mu.Lock()
	defer d.mu.Unlock()
	room := d.byRoom[key]
	if room == nil {
		room = map[int32]*mesoDrop{}
		d.byRoom[key] = room
	}
	room[objectID] = drop
	for _, sink := range d.sinks[key] {
		_ = sink.WritePacket(packet)
	}
	return packet
}

// ClaimMeso marks a drop claimed and returns amount/pickup op if this caller won the race.
func (d *dropSpace) ClaimMeso(channel, mapID int, objectID int32, x, y int) (amount int32, ownerID int64, pickupOp string, ok bool) {
	key := mapKey{channel: channel, mapID: mapID}
	d.mu.Lock()
	defer d.mu.Unlock()
	room := d.byRoom[key]
	if room == nil {
		return 0, 0, "", false
	}
	drop := room[objectID]
	if drop == nil || drop.claimed || !within(x, y, drop.x, drop.y, 150) {
		return 0, 0, "", false
	}
	drop.claimed = true
	return drop.amount, drop.ownerID, drop.pickupOp, true
}

func (d *dropSpace) ReleaseClaim(channel, mapID int, objectID int32) {
	key := mapKey{channel: channel, mapID: mapID}
	d.mu.Lock()
	defer d.mu.Unlock()
	if room := d.byRoom[key]; room != nil {
		if drop := room[objectID]; drop != nil {
			drop.claimed = false
		}
	}
}

func (d *dropSpace) Remove(channel, mapID int, objectID, characterID int32) {
	key := mapKey{channel: channel, mapID: mapID}
	packet := protocol.RemoveItemPickup(objectID, characterID)
	d.mu.Lock()
	defer d.mu.Unlock()
	if room := d.byRoom[key]; room != nil {
		delete(room, objectID)
		if len(room) == 0 {
			delete(d.byRoom, key)
		}
	}
	for _, sink := range d.sinks[key] {
		_ = sink.WritePacket(packet)
	}
}
