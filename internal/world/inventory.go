package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) handleInventoryMove(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadInventoryMove(r)
	if err != nil {
		return err
	}
	repo, ok := s.repo.(domain.InventoryRepository)
	if !ok {
		return c.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	result, err := repo.MoveInventory(ctx, req)
	if err != nil {
		slog.Info("inventory_move_rejected", "character", req.Session.CharacterID, "inventory", req.InvType, "source", req.Source, "destination", req.Destination, "error", err)
		return c.WritePacket(protocol.EnableActions())
	}
	s.applyInventoryResult(live, result.Character)
	if result.Replayed {
		return c.WritePacket(protocol.EnableActions())
	}
	if err = c.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
		return err
	}
	if req.InvType == 1 && (req.Source < 0 || req.Destination < 0) {
		s.refreshCharacterLook(live)
		if err = c.WritePacket(protocol.InventoryVitals(result.Character)); err != nil {
			return err
		}
	}
	if result.Drop != nil {
		s.broadcastInventoryPacket(result.Drop.ChannelID, result.Drop.MapID, protocol.DropInventoryItem(*result.Drop))
		s.offerZakumEye(ctx, req.Session, *result.Drop)
		s.offerPapulatusFragment(ctx, req.Session, *result.Drop)
	}
	if req.InvType == 1 && (req.Source == -18 || req.Source == -19) && req.Destination >= 0 {
		s.dismount(result.Character)
	}
	return c.WritePacket(protocol.EnableActions())
}

// dismount cancels the riding buff when the mount or saddle is taken off
// (Java cancelEffectFromBuffStat(MONSTER_RIDING) on unequipping -18).
func (s *Server) dismount(c domain.Character) {
	now := time.Now()
	for _, b := range c.SkillBuffs {
		if b.Stat == "MONSTER_RIDING" && b.ExpiresAt.After(now) {
			s.applyExternalBuff(c.ID, domain.SkillBuffChange{SkillID: b.ID, Level: b.Level, Cancel: true})
			return
		}
	}
}

// Item drops own the high object-ID range; all other objects fall through to
// existing meso handling. A failed pickup never removes the map object.
func (s *Server) handleInventoryPickup(ctx context.Context, live *liveCharacter, c packetSink, objectID int32) (bool, error) {
	if objectID < 1000000000 {
		return false, nil
	}
	repo, ok := s.repo.(domain.InventoryRepository)
	if !ok {
		return true, c.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	result, err := repo.PickupItem(ctx, domain.ItemPickupRequest{Session: session, ObjectID: objectID})
	if err != nil {
		slog.Debug("inventory_pickup_rejected", "character", session.CharacterID, "error", err)
		return true, c.WritePacket(protocol.EnableActions())
	}
	s.applyInventoryResult(live, result.Character)
	if result.Replayed {
		return true, c.WritePacket(protocol.EnableActions())
	}
	if len(result.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
			return true, err
		}
	}
	if result.Drop != nil && result.Drop.Mesos > 0 {
		if err = c.WritePacket(protocol.UpdateMesos(result.Character.Mesos, true)); err != nil {
			return true, err
		}
	}
	if packet := pickupGainNotification(result); packet != nil {
		if err = c.WritePacket(packet); err != nil {
			return true, err
		}
	}
	if result.Drop != nil {
		s.removeVisibleDrop(*result.Drop, protocol.RemoveItemPickup(objectID, int32(session.CharacterID)))
	} else {
		s.broadcastInventoryPacket(session.ChannelID, session.Character.MapID, protocol.RemoveItemPickup(objectID, int32(session.CharacterID)))
	}
	live.mu.Lock()
	current := live.session
	live.mu.Unlock()
	if err = s.refreshQuestDrops(ctx, current, c); err != nil {
		return true, err
	}
	return true, c.WritePacket(protocol.EnableActions())
}

func (s *Server) applyInventoryResult(live *liveCharacter, value domain.Character) {
	live.mu.Lock()
	c := &live.session.Character
	if value.Revision < c.Revision {
		live.mu.Unlock()
		return
	}
	// Idempotent replies may read older checkpointed HP/MP at the same asset
	// revision. Keep live damage in that case; only new commits replace vitals.
	if value.Revision > c.Revision {
		c.HP = value.HP
		c.MP = value.MP
		c.Quests = value.Quests
		c.QuestRecords = value.QuestRecords
	}
	c.Equipment = value.Equipment
	c.Items = value.Items
	c.InventorySlots = value.InventorySlots
	c.Mesos = value.Mesos
	c.MaxHP = value.MaxHP
	c.MaxMP = value.MaxMP
	c.BaseMaxHP, c.BaseMaxMP, c.VitalsModel, c.VitalsOrigin = value.BaseMaxHP, value.BaseMaxMP, value.VitalsModel, value.VitalsOrigin
	c.Mesos = value.Mesos
	c.Revision = value.Revision
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	s.updateQuestDropSession(snapshot)
	s.refreshParty(live)
}

func (s *Server) sendInventoryDrops(ctx context.Context, session domain.CharacterSession, c packetSink) error {
	repo, ok := s.repo.(domain.InventoryRepository)
	if !ok {
		return nil
	}
	drops, err := repo.ListItemDrops(ctx, session.Lease.WorldID, session.ChannelID, session.Character.MapID)
	if err != nil {
		return err
	}
	for _, drop := range drops {
		if err = s.reconcileQuestDrop(session, c, drop); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) broadcastInventoryPacket(channel, mapID int, packet []byte) {
	if s.drops == nil || packet == nil {
		return
	}
	s.drops.mu.Lock()
	defer s.drops.mu.Unlock()
	for _, sink := range s.drops.sinks[mapKey{channel: channel, mapID: mapID}] {
		_ = sink.WritePacket(packet)
	}
}

func (s *Server) refreshCharacterLook(live *liveCharacter) {
	v := s.sceneFor(live, nil)
	if v == nil {
		return
	}
	unlock, err := s.lockScenePublication(v.owner.CharacterID)
	if err != nil {
		return
	}
	defer unlock()
	s.refreshCharacterLookScene(v)
}

// Caller holds this actor's scene gate. GM already holds map publication and
// must call this helper, never acquire a scene gate inside global publication.
func (s *Server) refreshCharacterLookScene(v *sceneVisit) {
	if s.maps == nil || !s.sceneCurrent(v) {
		return
	}
	v.live.mu.Lock()
	snapshot := v.live.session
	v.live.mu.Unlock()
	s.maps.mu.Lock()
	defer s.maps.mu.Unlock()
	room := s.maps.rooms[mapKey{channel: snapshot.ChannelID, mapID: snapshot.Character.MapID}]
	self := room[snapshot.CharacterID]
	if self == nil || !sameDropSink(self.sink, v.sink) {
		return
	}
	refreshDragonLocked(room, self, snapshot.Character.Job) // GM job changes arrive here.
	self.look = snapshot.Character
	for peerID := range self.visible {
		if peer := room[peerID]; peer != nil {
			_ = peer.sink.WritePacket(protocol.CharacterLook(snapshot.Character))
		}
	}
}
