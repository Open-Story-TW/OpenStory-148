package world

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"os"
	"time"
)

// No database reads. Pin before waiting: a same-socket/same-map re-entry must
// not accept a movement operation already waiting for the previous visit.
func (s *Server) moveScene(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	v := s.sceneFor(live, sink)
	if v == nil {
		return domain.ErrStaleOwner
	}
	return s.moveSceneVisit(v, r)
}

// Split only to make delayed callback/old-visit rejection deterministic in tests.
// The production entry always pins through sceneFor before waiting for the gate.
func (s *Server) moveSceneVisit(v *sceneVisit, r *protocol.Reader) error {
	if v == nil {
		return domain.ErrStaleOwner
	}
	live, sink := v.live, v.sink
	ground, _ := content.RecoveryGroundSupport(os.Getenv("MAPLEGO_WZ_ROOT"), v.owner.Character.MapID)
	unlock, err := s.lockScenePublication(v.owner.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(v) {
		return nil
	}
	live.mu.Lock()
	move, err := protocol.ApplyPlayerMove(r, &live.session.Character)
	if err != nil {
		live.mu.Unlock()
		return err
	}
	if move.Changed {
		live.dirty = true
	}
	live.movedAt = time.Now()
	snapshot := live.session
	live.mu.Unlock()
	if len(move.Fragments) > 0 {
		s.observeRecoveryMovement(v, snapshot, move, ground, time.Now())
	}
	if !s.updateOnlineOwned(snapshot, snapshot) {
		return domain.ErrStaleOwner
	}
	if len(move.Fragments) > 0 && s.maps != nil {
		s.maps.Move(snapshot.ChannelID, snapshot.Character.MapID, snapshot.CharacterID, snapshot.Character, move.StartX, move.StartY, move.Fragments, sink)
		s.refreshParty(live)
	}
	return nil
}
