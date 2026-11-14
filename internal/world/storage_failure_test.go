package world

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type storageRejectRepo struct {
	domain.Repository
	failure  error
	requests []domain.StorageRequest
}

func (r *storageRejectRepo) OpenStorage(context.Context, domain.CharacterSession) (domain.Storage, error) {
	return domain.Storage{}, nil
}
func (r *storageRejectRepo) ChangeStorage(_ context.Context, q domain.StorageRequest) (domain.StorageResult, error) {
	r.requests = append(r.requests, q)
	return domain.StorageResult{}, r.failure
}

func TestStorageRejectedRequestsClearNativeWindowBusyWithoutInventingAssetsOrCapacity(t *testing.T) {
	unknown := errors.New("commit outcome unknown")
	for _, test := range []struct {
		name        string
		failure     error
		full, fatal bool
		want        byte
	}{
		{"funds", domain.ErrInsufficient, false, false, 14}, {"capacity", domain.ErrInsufficient, true, false, 17},
		{"expired_or_invalid", domain.ErrInvalidInput, false, false, 14}, {"missing", domain.ErrNotFound, false, false, 14},
		{"stale", domain.ErrStaleOwner, false, true, 14}, {"forbidden", domain.ErrForbidden, false, true, 14}, {"unknown_commit", unknown, false, true, 14},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &storageRejectRepo{failure: test.failure}
			character := domain.Character{ID: 7, AccountID: 8, WorldID: 0, MapID: 1000000, Mesos: 200, HP: 50, Revision: 9, Items: []domain.InventoryItem{{InvType: 2, Slot: 1, TemplateID: 2000000, Quantity: 3}}}
			live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, SessionID: "storage-session", Generation: 2, Character: character}}
			s := &Server{repo: repo, economyDialogs: newEconomyDialogSpace()}
			dialog := &economyDialog{id: "storage-op", live: live, kind: "storage", storage: domain.Storage{Capacity: 4, Mesos: 100, Revision: 11}}
			if test.full {
				dialog.storage.Capacity = 1
				dialog.storage.Items = []domain.StoredItem{{ID: 99, Item: domain.InventoryItem{InvType: 4, TemplateID: 4000000, Quantity: 1}}}
			}
			s.setEconomyDialog(live, dialog)
			request := protocol.NewWriter(0)
			request.U8(5)
			request.I16(1)
			request.I32(2000000)
			request.I16(1)
			sink := &memSink{}
			err := s.handleStorage(context.Background(), live, sink, protocol.NewReader(request.Packet()[2:]))
			if test.fatal {
				if !errors.Is(err, test.failure) {
					t.Fatalf("fatal error lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(sink.pkts) != 1 || !reflect.DeepEqual(sink.pkts[0], []byte{0x46, 0x02, test.want}) {
				t.Fatalf("native rejection packet %x", sink.pkts)
			}
			if len(repo.requests) != 1 || repo.requests[0].OperationID != "storage-op/1" || repo.requests[0].Session.Generation != 2 {
				t.Fatalf("request was replayed or authority changed: %+v", repo.requests)
			}
			if !reflect.DeepEqual(live.session.Character, character) || dialog.storage.Revision != 11 || dialog.sequence != 1 {
				t.Fatal("rejected request mutated live assets/storage revision")
			}
			if test.fatal {
				if s.getEconomyDialog(live, "storage") != nil {
					t.Fatal("stale/unknown authority retained")
				}
				_ = s.handleStorage(context.Background(), live, sink, protocol.NewReader(request.Packet()[2:]))
				if len(repo.requests) != 1 {
					t.Fatal("unknown outcome was replayed under a new operation")
				}
			} else if s.getEconomyDialog(live, "storage") != dialog {
				t.Fatal("known rollback unexpectedly removed current dialog")
			}
		})
	}
}

func TestOldStorageConnectionCannotUseReplacementDialog(t *testing.T) {
	repo := &storageRejectRepo{failure: domain.ErrInsufficient}
	old := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, Generation: 1}}
	next := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, Generation: 2}}
	s := &Server{repo: repo, economyDialogs: newEconomyDialogSpace()}
	dialog := &economyDialog{live: next, kind: "storage"}
	s.setEconomyDialog(next, dialog)
	sink := &memSink{}
	if err := s.handleStorage(context.Background(), old, sink, protocol.NewReader([]byte{7, 1, 0, 0, 0})); err != nil {
		t.Fatal(err)
	}
	if len(repo.requests) != 0 || s.getEconomyDialog(next, "storage") != dialog || !reflect.DeepEqual(sink.pkts[0], protocol.StorageRejected()) {
		t.Fatal("old connection touched replacement authority or left its own native busy request unanswered")
	}
}
