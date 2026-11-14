package world

import (
	"context"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type playerMesoTestRepo struct {
	domain.Repository
	request domain.MesoDropRequest
}

func (r *playerMesoTestRepo) DropMesosOnMap(_ context.Context, request domain.MesoDropRequest) (domain.InventoryResult, error) {
	r.request = request
	c := request.Session.Character
	c.Mesos -= request.Amount
	c.Revision++
	return domain.InventoryResult{Character: c, Drop: &domain.ItemDrop{ObjectID: 1000000010, WorldID: c.WorldID, ChannelID: request.Session.ChannelID, MapID: c.MapID, OwnerID: c.ID, Mesos: request.Amount, PlayerDrop: true, Quantity: 1, X: c.X, Y: c.Y}}, nil
}

func TestPlayerMesoDropUsesAuthenticatedOwnerAndBroadcastsCommittedObject(t *testing.T) {
	repo := &playerMesoTestRepo{}
	s := &Server{repo: repo, drops: newDropSpace(), online: make(map[int64]domain.CharacterSession)}
	live := &liveCharacter{session: domain.CharacterSession{AccountID: 3, CharacterID: 1, SessionID: "live-meso", Generation: 2, ChannelID: 1, Character: domain.Character{ID: 1, AccountID: 3, Mesos: 1000, HP: 50, MaxHP: 50, MapID: 10000, X: 20, Y: 40, Revision: 1}}}
	self, peer := &memSink{}, &memSink{}
	s.drops.Track(1, 10000, 1, self)
	s.drops.Track(1, 10000, 2, peer)
	handled, err := s.dropPlayerMesos(context.Background(), live, self, 12345, 100)
	if err != nil || !handled || repo.request.Session.CharacterID != 1 || repo.request.Session.AccountID != 3 || len(repo.request.OperationID) < 16 || live.session.Character.Mesos != 900 || live.session.Character.Revision != 2 {
		t.Fatalf("drop: handled=%t error=%v request=%+v", handled, err, repo.request)
	}
	if len(self.pkts) != 2 || self.opcodes()[0] != protocol.SendDropItem || self.opcodes()[1] != protocol.SendUpdateStats || len(peer.pkts) != 1 || peer.opcodes()[0] != protocol.SendDropItem {
		t.Fatalf("drop visibility: self=%v peer=%v", self.opcodes(), peer.opcodes())
	}
}
