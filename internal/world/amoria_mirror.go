package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) tickAmoria(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	var active []*eventInstance
	for _, inst := range s.events.running {
		if inst.amoria != nil && !inst.ended && now.Before(inst.currentDeadline()) {
			active = append(active, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range active {
		if len(s.activeEventMembers(inst, content.AmoriaMirror)) == 0 {
			continue
		}
		step, cancel := context.WithTimeout(ctx, 450*time.Millisecond)
		s.tryAmoriaMirror(step, inst)
		cancel()
	}
}

func (s *Server) tryAmoriaMirror(ctx context.Context, inst *eventInstance) {
	reader, read := s.repo.(domain.InventoryRepository)
	writer, write := s.repo.(domain.EventDropTransformer)
	if !read || !write {
		return
	}
	inst.amoria.mu.Lock()
	defer inst.amoria.mu.Unlock()
	if !inst.amoria.fairy || inst.amoria.mirrorCleared {
		return
	}
	const mapID = content.AmoriaMirror
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, mapID}]
	if room == nil {
		g.mu.Unlock()
		return
	}
	var mirror *roomReactor
	for _, r := range room.reactors {
		if r.def.TemplateID == 6702000 && r.state == 0 {
			mirror = r
			break
		}
	}
	g.mu.Unlock()
	if mirror == nil {
		return
	}
	drops, err := reader.ListItemDrops(ctx, s.cfg.WorldID, inst.channel, mapID)
	if err != nil {
		return
	}
	position := mirror.def
	// The v148 reactor's type-100 event accepts the hammer in this box.
	for _, drop := range drops {
		dx, dy := drop.X-position.X, drop.Y-position.Y
		if !drop.PlayerDrop || drop.TemplateID != content.AmoriaHammer || drop.Quantity != 1 ||
			dx < -144 || dx > 144 || dy < -34 || dy > 14 || s.eventOf(drop.OwnerID) != inst {
			continue
		}
		x, y := s.gatherDropPoint(mapID, position.X, position.Y, 12)
		shard, applied, err := writer.TransformEventDrop(ctx, drop, content.AmoriaShard, x, y)
		if err != nil || !applied {
			continue
		}
		s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		g.mu.Lock()
		mirror.state = 1
		packets := roomBroadcast(room, protocol.ReactorState(mirror.objectID, 1, position.X, position.Y), -1)
		g.mu.Unlock()
		deliver(packets)
		s.broadcastCombatDrop(shard)
		return
	}
}
