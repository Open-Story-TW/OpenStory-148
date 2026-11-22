package world

import (
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const qaMobRoomLimit = 48
const qaMobReceiptLimit = 256

type qaMobOwner struct {
	owner   domain.CharacterSession
	member  *gameplayMember
	batch   string
	expires time.Time
	retired bool
}

type qaMobOutcome struct {
	owner   domain.CharacterSession
	name    string
	args    []int64
	ids     []int32
	expires time.Time
}

type qaMobDelivery struct {
	key         mapKey
	characterID int64
	member      *gameplayMember
	packets     [][]byte
	publication protocol.PacketPublication
	err         error
}

// A flat, real foothold is required for the initial QA field. Body bounds must
// fit entirely inside its patrol interval; unsupported/flying-only geometry is
// rejected rather than assigning guessed coordinates or synthetic HP.
func qaMobPositions(m content.Map, body content.MobBodyBounds, n, nearX, nearY int) ([]content.Life, error) {
	return mobPositions(m, body, n, nearX, nearY, false)
}

// mobPositions is qaMobPositions with the field policy optional: anyMap lets a
// standing GM spawn on an ordinary map, beside its own monsters.
func mobPositions(m content.Map, body content.MobBodyBounds, n, nearX, nearY int, anyMap bool) ([]content.Life, error) {
	if !anyMap && !domain.IsQAMobField(m.ID) || n < 1 || n > domain.QAMobMaxBatch || !body.Envelope.Nonempty() || body.FrameCount < 1 {
		return nil, domain.ErrInvalidInput
	}
	for _, l := range m.Life {
		if l.Kind == "m" && !anyMap && m.ID != domain.QAPharaohMap {
			return nil, domain.ErrForbidden
		}
	}
	var flats []content.Foothold
	for _, fh := range m.Footholds {
		if fh.ID <= 0 || fh.ID > 32767 || fh.Y1 != fh.Y2 || fh.Y1 < -32000 || fh.Y1 > 32000 {
			continue
		}
		if fh.X1 > fh.X2 {
			fh.X1, fh.X2 = fh.X2, fh.X1
		}
		if fh.X1 == fh.X2 {
			continue
		}
		flats = append(flats, fh)
	}
	sort.Slice(flats, func(i, j int) bool {
		a, b := flats[i], flats[j]
		if a.Y1 != b.Y1 {
			return a.Y1 < b.Y1
		}
		if a.X1 != b.X1 {
			return a.X1 < b.X1
		}
		return a.ID < b.ID
	})
	type span struct {
		left, right, y int
		pieces         []content.Foothold
	}
	var spans []span
	for _, fh := range flats {
		n := len(spans)
		if n > 0 && spans[n-1].y == fh.Y1 && fh.X1 <= spans[n-1].right {
			p := &spans[n-1]
			p.right = max(p.right, fh.X2)
			p.pieces = append(p.pieces, fh)
		} else {
			spans = append(spans, span{fh.X1, fh.X2, fh.Y1, []content.Foothold{fh}})
		}
	}
	// The warp portal is above the ground. Prefer the nearest real surface
	// below the actor, not an unrelated upper platform whose y sorts first.
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].y < spans[j].y })
	for _, floor := range spans {
		if floor.y < nearY-8 {
			continue
		}
		lo, hi := floor.left+8-min(0, body.Envelope.Left), floor.right-8-max(0, body.Envelope.Right)
		width := (n - 1) * 32
		if lo < -32000 || hi > 32000 || hi-lo < width {
			continue
		}
		start := max(lo, min(nearX-width/2, hi-width))
		out := make([]content.Life, n)
		for i := range out {
			x := start + i*32
			id := 0
			for _, piece := range floor.pieces {
				if x >= piece.X1 && x <= piece.X2 {
					id = piece.ID
					break
				}
			}
			if id == 0 {
				return nil, domain.ErrInvalidInput
			}
			out[i] = content.Life{Kind: "m", X: x, Y: floor.y - 1, Foothold: id, RX0: lo, RX1: hi}
		}
		return out, nil
	}
	return nil, domain.ErrInvalidInput
}

// Explicit placement uses the same real floor/body fit as the original mode,
// but cannot silently shift the requested group center to fit that floor.
// The actor supplies only the existing nearY surface selection, never a new Y.
func qaMobPositionsAtCenter(m content.Map, body content.MobBodyBounds, n, centerX, nearY int) ([]content.Life, error) {
	return mobPositionsAtCenter(m, body, n, centerX, nearY, false)
}

func mobPositionsAtCenter(m content.Map, body content.MobBodyBounds, n, centerX, nearY int, anyMap bool) ([]content.Life, error) {
	if centerX < -32000 || centerX > 32000 {
		return nil, domain.ErrInvalidInput
	}
	lives, err := mobPositions(m, body, n, centerX, nearY, anyMap)
	if err != nil {
		return nil, err
	}
	start := centerX - 16*(n-1)
	for i, life := range lives {
		if life.X != start+32*i {
			return nil, domain.ErrInvalidInput
		}
	}
	return lives, nil
}

// Requires persistMu and the exact CID scene gate. SQL has already authorized
// this operation; no SQL or packet writes occur under gameplay.mu.
func (s *Server) publishQAMobCommand(req domain.GMCommandRequest, result domain.GMCommandResult, visit *sceneVisit, sink packetSink) error {
	now := time.Now()
	a := result.QAMobs
	if s.gameplay == nil || a == nil || result.Denied || result.Changed || result.MapChanged || result.Name != req.Name || !slices.Equal(result.Args, req.Args) || a.RuntimeID != req.RuntimeID || req.RuntimeID != s.gameplay.bootID || a.GrantRevision <= 0 || !a.ExpiresAt.After(now) || a.ExpiresAt.After(now.Add(domain.QAMobLifetime)) || !domain.ValidGMMobCommand(req.Name, req.Args) || !result.Standing && !domain.IsQAMobField(req.Session.Character.MapID) || result.Character.ID != req.Session.CharacterID || result.Character.AccountID != req.Session.AccountID || result.Character.WorldID != req.Session.Lease.WorldID || result.Character.MapID != req.Session.Character.MapID || result.Character.Revision != req.Session.Character.Revision || result.Character.HP <= 0 || !s.sceneCurrent(visit) || !s.gmCurrent(req.Session, visit.live, sink) {
		return domain.ErrStaleOwner
	}
	g := s.gameplay
	key := mapKey{channel: req.Session.ChannelID, mapID: req.Session.Character.MapID}
	var lives []content.Life
	var mob content.Mob
	if req.Name == "spawn" {
		// File parsing is outside gameplay.mu and cannot stall other rooms.
		m, err := g.catalog.Map(key.mapID)
		if err != nil {
			return qaMobDenied(sink, "QA map unavailable")
		}
		mob, err = g.catalog.Mob(int(req.Args[0]))
		if err != nil || mob.HP <= 0 {
			return qaMobDenied(sink, "WZ monster unavailable")
		}
		body, err := g.catalog.MobBodyBounds(mob.ID)
		if err != nil {
			return qaMobDenied(sink, "WZ monster geometry unavailable")
		}
		if len(req.Args) == 3 {
			lives, err = mobPositionsAtCenter(m, body, int(req.Args[1]), int(req.Args[2]), req.Session.Character.Y, result.Standing)
		} else {
			lives, err = mobPositions(m, body, int(req.Args[1]), req.Session.Character.X, req.Session.Character.Y, result.Standing)
		}
		if err != nil {
			return qaMobDenied(sink, "monster does not fit a flat foothold below you")
		}
		for i := range lives {
			lives[i].TemplateID = mob.ID
		}
	}
	now = time.Now()
	if !a.ExpiresAt.After(now) {
		return domain.ErrStaleOwner
	}
	g.mu.Lock()
	ids, deliveries, err := g.applyQAMobsLocked(key, req, a.ExpiresAt, visit.live, sink, mob, lives, now)
	reserveQAMobDeliveries(deliveries)
	g.mu.Unlock()
	if err != nil {
		if err == domain.ErrStaleOwner {
			return err
		}
		return qaMobDenied(sink, "QA mob capacity, pending combat or runtime receipt unavailable; nothing changed")
	}
	for _, d := range deliveries {
		if d.err != nil {
			if c, ok := d.member.sink.(interface{ Close() error }); ok {
				_ = c.Close()
			}
			continue
		}
		g.mu.Lock()
		r := g.rooms[d.key]
		current := r != nil && r.members[d.characterID] == d.member
		g.mu.Unlock()
		if !current {
			if d.publication != nil {
				d.publication.Abort(domain.ErrStaleOwner)
			}
			continue
		}
		{
			var e error
			if d.publication != nil {
				e = d.publication.WritePackets(d.packets)
			} else {
				e = writeQAMobPackets(d.member.sink, d.packets)
			}
			if e != nil {
				slog.Warn("qa_mob_publication_failed", "batch", req.OperationID, "error", e)
			}
		}
	}
	slog.Info("qa_mob_runtime_applied", "command", req.Name, "character", req.Session.CharacterID, "generation", req.Session.Generation, "channel", key.channel, "map", key.mapID, "batch", req.OperationID, "object_ids", ids, "expires", a.ExpiresAt)
	return gmFeedback(sink, qaMobFeedback(req.Name, req.OperationID, ids))
}

func qaMobDenied(sink packetSink, reason string) error {
	if err := gmFeedback(sink, "Denied: no active dedicated QA grant or unsupported state."); err != nil {
		return err
	}
	return gmFeedback(sink, "Reason: "+reason+".")
}

// Reserve, never wait, under the same mutex as the object mutation. Real Conn
// writers then order expiry/removal/next-command frames after this spawn batch
// even if a slow socket delays the lock-free flush. Failures close outside mu.
func reserveQAMobDeliveries(out []qaMobDelivery) {
	for i := range out {
		if sink, ok := out[i].member.sink.(tradePublicationSink); ok {
			out[i].publication, out[i].err = sink.ReservePublication()
		}
	}
}

func qaMobFeedback(name, batch string, ids []int32) string {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.FormatInt(int64(id), 10)
	}
	text := strings.Join(values, ",")
	if text == "" {
		text = "none"
	}
	return fmt.Sprintf("Committed %s; object_ids=%s; batch=%s; setup provenance: gm_command.", name, text, batch)
}

func (g *gameplaySpace) applyQAMobsLocked(key mapKey, req domain.GMCommandRequest, expires time.Time, live *liveCharacter, sink packetSink, stats content.Mob, lives []content.Life, now time.Time) ([]int32, []qaMobDelivery, error) {
	r := g.rooms[key]
	if r == nil {
		return nil, nil, domain.ErrStaleOwner
	}
	m := r.members[req.Session.CharacterID]
	if m == nil || m.live != live || !sameDropSink(m.sink, sink) || m.sessionID != req.Session.SessionID || m.generation != req.Session.Generation || m.worldID != req.Session.Lease.WorldID {
		return nil, nil, domain.ErrStaleOwner
	}
	if g.qaMobOutcomes == nil {
		g.qaMobOutcomes = make(map[string]qaMobOutcome)
	}
	for id, old := range g.qaMobOutcomes {
		if !now.Before(old.expires) {
			delete(g.qaMobOutcomes, id)
		}
	}
	if old, ok := g.qaMobOutcomes[req.OperationID]; ok {
		if !sameSceneOwner(old.owner, req.Session) || old.owner.Character.MapID != key.mapID || old.name != req.Name || !slices.Equal(old.args, req.Args) {
			return nil, nil, domain.ErrForbidden
		}
		return append([]int32(nil), old.ids...), nil, nil
	}
	if len(g.qaMobOutcomes) >= qaMobReceiptLimit {
		return nil, nil, domain.ErrForbidden
	}
	var ids []int32
	var out []qaMobDelivery
	appendPacket := func(packet []byte) {
		for id, p := range r.members {
			out = append(out, qaMobDelivery{key: key, characterID: id, member: p, packets: [][]byte{packet}})
		}
	}
	if req.Name == "spawn" {
		owned, total := 0, 0
		for _, mob := range r.mobs {
			if mob.qaOwner != nil {
				total++
				if sameSceneOwner(mob.qaOwner.owner, req.Session) {
					owned++
				}
			}
		}
		if len(lives) != int(req.Args[1]) || owned+len(lives) > domain.QAMobMaxBatch || total+len(lives) > qaMobRoomLimit || int64(g.nextID)+int64(len(lives)) >= 999999999 {
			return nil, nil, domain.ErrForbidden
		}
		owner := req.Session
		owner.Character = domain.Character{MapID: key.mapID}
		marker := &qaMobOwner{owner: owner, member: m, batch: req.OperationID, expires: expires}
		for _, life := range lives {
			oid := g.alloc()
			mob := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, controller: req.Session.CharacterID, qaOwner: marker}
			r.mobs[oid] = mob
			ids = append(ids, oid)
			appendPacket(mob.spawnPacket(false, now))
			out = append(out, qaMobDelivery{key: key, characterID: req.Session.CharacterID, member: m, packets: [][]byte{mob.spawnPacket(true, now)}})
		}
	} else {
		// All or none: never remove an object reserved by an uncertain SQL
		// settlement. The operator can issue a NEW command after it resolves.
		for _, mob := range r.mobs {
			if mob.qaOwner != nil && sameSceneOwner(mob.qaOwner.owner, req.Session) && mob.qaOwner.member == m && (mob.claiming || mob.reservation != nil) {
				return nil, nil, domain.ErrForbidden
			}
		}
		for oid, mob := range r.mobs {
			if mob.qaOwner == nil || !sameSceneOwner(mob.qaOwner.owner, req.Session) || mob.qaOwner.member != m {
				continue
			}
			if mob.hp > 0 {
				ids = append(ids, oid)
				for id, p := range r.members {
					out = append(out, qaMobDelivery{key: key, characterID: id, member: p, packets: qaMobRemovalPackets(mob, id)})
				}
			}
			delete(r.mobs, oid)
		}
		for oid, area := range r.areas {
			if area.ownerID != req.Session.CharacterID || area.ownerSession != req.Session.SessionID || area.ownerGeneration != req.Session.Generation || area.reservation != nil {
				continue
			}
			delete(r.areas, oid)
			appendPacket(protocol.AffectedAreaRemoved(area.objectID, area.skillID, false))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	owner := req.Session
	owner.Character = domain.Character{MapID: key.mapID}
	g.qaMobOutcomes[req.OperationID] = qaMobOutcome{owner, req.Name, append([]int64(nil), req.Args...), append([]int32(nil), ids...), expires}
	return ids, out, nil
}

// Retire only this exact member's batches. An unresolved combat ticket keeps
// its object until resolution; the expiry worker retries without altering HP.
func retireQAMobOwnerLocked(r *gameplayRoom, member *gameplayMember) {
	for _, mob := range r.mobs {
		if mob.qaOwner == nil || mob.qaOwner.member != member {
			continue
		}
		mob.qaOwner.retired = true
	}
}

// The native reason-0 leave preserves an active controller's CMob. Revoke
// that exact current controller first; observers only need the normal leave.
// Build under gameplay.mu, while controller and recipient membership are pinned.
// Both frames share one ordered publication, without changing death semantics.
func qaMobRemovalPackets(mob *gameplayMob, recipient int64) [][]byte {
	out := make([][]byte, 0, 2)
	if mob.controller != 0 && mob.controller == recipient {
		out = append(out, protocol.StopMonsterControl(mob.objectID))
	}
	return append(out, protocol.RemoveMonster(mob.objectID))
}

// Real connections publish the pair in one slot. Test/alternate sinks stop on
// the first failure, so a failed control revocation never falls through to leave.
// Call only outside gameplay.mu, under the existing publication/scene contract.
func writeQAMobPackets(sink packetSink, packets [][]byte) error {
	if ordered, ok := sink.(tradePublicationSink); ok {
		publication, err := ordered.ReservePublication()
		if err != nil {
			return err
		}
		return publication.WritePackets(packets)
	}
	for _, packet := range packets {
		if err := sink.WritePacket(packet); err != nil {
			return err
		}
	}
	return nil
}

type qaMobCleanup struct {
	key         mapKey
	characterID int64
	member      *gameplayMember
	visit       *sceneVisit
	packets     [][]byte
}

// Only the gameplay worker calls this, outside any owner's scene gate. Leaving
// a field merely retires the marker so it never takes a peer gate while holding
// its own: two concurrent departures cannot form a cross-CID lock cycle.
func (s *Server) expireQAMobs(now time.Time) {
	if s.gameplay == nil {
		return
	}
	out := s.gameplay.collectExpiredQAMobs(now)
	s.flushQAMobCleanup(out)
}

func (g *gameplaySpace) collectExpiredQAMobs(now time.Time) []qaMobCleanup {
	var out []qaMobCleanup
	g.mu.Lock()
	for key, r := range g.rooms {
		for oid, mob := range r.mobs {
			a := mob.qaOwner
			if a == nil || !a.retired && now.Before(a.expires) && r.members[a.owner.CharacterID] == a.member {
				continue
			}
			a.retired = true
			if mob.claiming || mob.reservation != nil {
				continue
			}
			if mob.hp > 0 {
				for id, p := range r.members {
					out = append(out, qaMobCleanup{key, id, p, p.visit, qaMobRemovalPackets(mob, id)})
				}
			}
			delete(r.mobs, oid)
		}
	}
	for id, old := range g.qaMobOutcomes {
		if !now.Before(old.expires) {
			delete(g.qaMobOutcomes, id)
		}
	}
	g.mu.Unlock()
	return out
}

func (s *Server) flushQAMobCleanup(out []qaMobCleanup) {
	for _, d := range out {
		if d.visit == nil {
			continue
		} // Missing exact visit is unknown, never a fallback to current.
		unlock, err := s.lockScenePublication(d.characterID)
		if err != nil {
			continue
		}
		if !s.sceneCurrent(d.visit) {
			unlock()
			continue
		}
		s.gameplay.mu.Lock()
		r := s.gameplay.rooms[d.key]
		current := r != nil && r.members[d.characterID] == d.member && d.member.visit == d.visit && sameDropSink(d.member.sink, d.visit.sink)
		s.gameplay.mu.Unlock()
		// The exact CID gate remains held through WritePacket and its Conn
		// ordered slot. A new SetField/re-entry cannot overtake this removal.
		if current {
			_ = writeQAMobPackets(d.visit.sink, d.packets)
		}
		unlock()
	}
}
