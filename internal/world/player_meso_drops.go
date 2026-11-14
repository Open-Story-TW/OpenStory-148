package world

import (
	"context"
	"crypto/sha256"
	"fmt"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Native ticks identify a request within this authenticated login generation.
// Position, owner, and the durable operation namespace never come from wire.
func (s *Server) dropPlayerMesos(ctx context.Context, live *liveCharacter, sink packetSink, tick uint32, amount int64) (bool, error) {
	repo, ok := s.repo.(domain.MesoDropRepository)
	if !ok {
		return false, nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	snapshot := live.session
	snapshot.Lease = s.currentLease()
	live.mu.Unlock()
	id := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%d/%d", snapshot.SessionID, snapshot.Generation, snapshot.CharacterID, tick)))
	result, err := repo.DropMesosOnMap(ctx, domain.MesoDropRequest{Session: snapshot, OperationID: fmt.Sprintf("player-meso-%x", id), Amount: amount})
	if err != nil {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	s.applyInventoryResult(live, result.Character)
	if !result.Replayed && result.Drop != nil {
		s.broadcastInventoryPacket(result.Drop.ChannelID, result.Drop.MapID, protocol.DropInventoryItem(*result.Drop))
	}
	return true, sink.WritePacket(protocol.UpdateMesos(result.Character.Mesos, true))
}
