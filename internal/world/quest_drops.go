package world

import (
	"context"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func questDropEligible(c domain.Character, d domain.ItemDrop) bool {
	remaining, err := content.QuestDropRemaining(os.Getenv("MAPLEGO_WZ_ROOT"), c, d.QuestID, d.TemplateID)
	return err == nil && remaining >= d.Quantity && d.Quantity > 0
}

// WZ evaluation happens before taking the drop mutex; this function never
// queries a database while holding a map/drop lock.
func (s *Server) reconcileQuestDrop(session domain.CharacterSession, sink packetSink, d domain.ItemDrop, expectedToken ...uint64) error {
	return s.reconcileQuestDropEntry(session, sink, d, protocol.DropInventoryItem, expectedToken...)
}

// The same quest visibility/identity transaction admits both an initial
// monster animation and a stationary reentry snapshot. Only the initial
// materialization supplies DropMonsterLoot; refresh must not replay a throw.
func (s *Server) reconcileQuestDropEntry(session domain.CharacterSession, sink packetSink, d domain.ItemDrop, spawnPacket func(domain.ItemDrop) []byte, expectedToken ...uint64) error {
	if d.QuestID == 0 {
		return sink.WritePacket(spawnPacket(d))
	}
	if s.drops == nil {
		if questDropEligible(session.Character, d) {
			return sink.WritePacket(spawnPacket(d))
		}
		return nil
	}
	key := mapKey{channel: session.ChannelID, mapID: session.Character.MapID}
	if d.ChannelID != key.channel || d.MapID != key.mapID {
		return nil
	}
	for attempt := 0; attempt < 4; attempt++ {
		s.drops.mu.Lock()
		track := s.drops.questTracks[key][session.CharacterID]
		if track == nil || !sameDropSink(track.sink, sink) || !sameDropSink(s.drops.sinks[key][session.CharacterID], sink) {
			s.drops.mu.Unlock()
			return nil
		}
		if len(expectedToken) > 0 && track.token != expectedToken[0] {
			s.drops.mu.Unlock()
			return nil
		}
		// Compatibility for existing isolated map tests; production Track calls
		// bind the authenticated session before any broadcast can be captured.
		if !track.bound {
			if !track.fixtureBinding {
				s.drops.mu.Unlock()
				return nil
			}
			track.session = cloneDropSession(session)
			track.bound = true
		}
		if track.session.SessionID != session.SessionID || track.session.Generation != session.Generation || track.session.Lease.WorldID != session.Lease.WorldID || d.WorldID != track.session.Lease.WorldID || session.Character.Revision < track.session.Character.Revision {
			s.drops.mu.Unlock()
			return nil
		}
		current, token, version := track.session, track.token, track.version
		s.drops.mu.Unlock()
		visible := questDropEligible(current.Character, d)
		s.drops.mu.Lock()
		track = s.drops.questTracks[key][session.CharacterID]
		if track == nil || track.token != token || !sameDropSink(track.sink, sink) || !sameDropSink(s.drops.sinks[key][session.CharacterID], sink) {
			s.drops.mu.Unlock()
			return nil
		}
		if track.version != version {
			s.drops.mu.Unlock()
			continue
		}
		if s.drops.questViews == nil {
			s.drops.questViews = map[mapKey]map[int64]map[int32]questDropDecision{}
		}
		room := s.drops.questViews[key]
		if room == nil {
			room = map[int64]map[int32]questDropDecision{}
			s.drops.questViews[key] = room
		}
		view := room[session.CharacterID]
		if view == nil {
			view = map[int32]questDropDecision{}
			room[session.CharacterID] = view
		}
		before := view[d.ObjectID]
		if before.revision > current.Character.Revision || before.version > version {
			s.drops.mu.Unlock()
			return nil
		}
		if before.visible != visible {
			packet := protocol.RemoveItemExpired(d.ObjectID)
			if visible {
				packet = spawnPacket(d)
			}
			if err := sink.WritePacket(packet); err != nil {
				s.drops.mu.Unlock()
				return err
			}
		}
		// Keep false decisions too: a late old broadcast cannot resurrect an
		// object after forfeit or collection completed without an earlier spawn.
		view[d.ObjectID] = questDropDecision{visible: visible, revision: current.Character.Revision, version: version}
		s.drops.mu.Unlock()
		return nil
	}
	return nil
}

func (s *Server) broadcastCombatDrop(d domain.ItemDrop) {
	if d.QuestID == 0 {
		s.broadcastInventoryPacket(d.ChannelID, d.MapID, protocol.DropMonsterLoot(d))
		return
	}
	if s.drops == nil {
		return
	}
	key := mapKey{channel: d.ChannelID, mapID: d.MapID}
	s.drops.mu.Lock()
	sinks := map[int64]packetSink{}
	tokens := map[int64]uint64{}
	for id, sink := range s.drops.sinks[key] {
		sinks[id] = sink
		if track := s.drops.questTracks[key][id]; track != nil {
			tokens[id] = track.token
		}
	}
	s.drops.mu.Unlock()
	s.mu.RLock()
	sessions := map[int64]domain.CharacterSession{}
	for id := range sinks {
		if session, ok := s.online[id]; ok {
			sessions[id] = session
		}
	}
	s.mu.RUnlock()
	for id, session := range sessions {
		if session.ChannelID == d.ChannelID && session.Character.MapID == d.MapID {
			_ = s.reconcileQuestDropEntry(session, sinks[id], d, protocol.DropMonsterLoot, tokens[id])
		}
	}
}

func (s *Server) removeVisibleDrop(d domain.ItemDrop, packet []byte) {
	if d.QuestID == 0 {
		s.broadcastInventoryPacket(d.ChannelID, d.MapID, packet)
		return
	}
	if s.drops == nil {
		return
	}
	key := mapKey{channel: d.ChannelID, mapID: d.MapID}
	s.drops.mu.Lock()
	defer s.drops.mu.Unlock()
	for id, sink := range s.drops.sinks[key] {
		if view := s.drops.questViews[key][id]; view != nil {
			if view[d.ObjectID].visible {
				_ = sink.WritePacket(packet)
			}
			delete(view, d.ObjectID)
		}
	}
}

// Refresh only quest objects after collection/quest transitions, never on
// movement or a per-player timer. Ordinary public drops are not resent.
func (s *Server) refreshQuestDrops(ctx context.Context, session domain.CharacterSession, sink packetSink, expectedToken ...uint64) error {
	repo, ok := s.repo.(domain.InventoryRepository)
	if !ok {
		return nil
	}
	if s.drops != nil && len(expectedToken) == 0 {
		key := mapKey{session.ChannelID, session.Character.MapID}
		s.drops.mu.Lock()
		track := s.drops.questTracks[key][session.CharacterID]
		if track != nil && sameDropSink(track.sink, sink) {
			expectedToken = []uint64{track.token}
		}
		s.drops.mu.Unlock()
		if len(expectedToken) == 0 {
			return nil
		}
	}
	drops, err := repo.ListItemDrops(ctx, session.Lease.WorldID, session.ChannelID, session.Character.MapID)
	if err != nil {
		return err
	}
	for _, d := range drops {
		if d.QuestID > 0 {
			if err = s.reconcileQuestDrop(session, sink, d, expectedToken...); err != nil {
				return err
			}
		}
	}
	return nil
}
