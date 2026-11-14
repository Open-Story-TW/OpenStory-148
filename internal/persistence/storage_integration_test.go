package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestStorageItemMesoReplayAndIsolation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	storage, err := s.OpenStorage(ctx, a)
	if err != nil || storage.Capacity != 48 || len(storage.Items) != 0 {
		t.Fatalf("open=%+v %v", storage, err)
	}
	req := domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: 2000000, Quantity: 2, ExpectedRevision: storage.Revision}
	result, err := s.ChangeStorage(ctx, req)
	if err != nil || result.Inventory.Character.Mesos != 900 || result.Inventory.Character.Items[0].Quantity != 3 || len(result.Storage.Items) != 1 || result.Storage.Items[0].Item.Quantity != 2 {
		t.Fatalf("deposit=%+v %v", result, err)
	}
	replay, err := s.ChangeStorage(ctx, req)
	if err != nil || !replay.Replayed || replay.Inventory.Character.Mesos != 900 {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	wrong := req
	wrong.Quantity++
	if _, err = s.ChangeStorage(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("altered replay=%v", err)
	}
	a.Character = result.Inventory.Character
	storage = result.Storage
	withdraw := domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: storage.Items[0].ID, ExpectedRevision: storage.Revision}
	result, err = s.ChangeStorage(ctx, withdraw)
	if err != nil || result.Inventory.Character.Items[0].Quantity != 5 || len(result.Storage.Items) != 0 {
		t.Fatalf("withdraw=%+v %v", result, err)
	}
	a.Character = result.Inventory.Character
	storage = result.Storage
	for i, delta := range []int64{-300, 200} {
		result, err = s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-mesos-%d-%d", a.CharacterID, i), Kind: "mesos", Delta: delta, ExpectedRevision: storage.Revision})
		if err != nil {
			t.Fatal(err)
		}
		a.Character = result.Inventory.Character
		storage = result.Storage
	}
	if a.Character.Mesos != 800 || storage.Mesos != 100 {
		t.Fatalf("mesos char%d storage%d", a.Character.Mesos, storage.Mesos)
	}
	other, err := s.OpenStorage(ctx, b)
	if err != nil || other.Mesos != 0 || len(other.Items) != 0 {
		t.Fatalf("account storage leaked=%+v %v", other, err)
	}
	if _, err = s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-overdraft-operation-%d", a.CharacterID), Kind: "mesos", Delta: 101, ExpectedRevision: storage.Revision}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("overdraft=%v", err)
	}
	loaded, err := s.OpenStorage(ctx, a)
	if err != nil || loaded.Mesos != 100 || loaded.Revision != storage.Revision {
		t.Fatalf("reload=%+v %v", loaded, err)
	}
}

func TestStoragePreservesEquipmentIdentity(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	weapon := equipmentAt(a.Character, -11)
	move, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 1, InvType: 1, Source: -11, Destination: 1, Quantity: -1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = move.Character
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-equip-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: weapon.ItemID, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil || result.Storage.Items[0].Equipment.ID != weapon.ID || equipmentAt(result.Inventory.Character, 1).ID != 0 {
		t.Fatalf("equipment deposit=%+v %v", result, err)
	}
	a.Character = result.Inventory.Character
	result, err = s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("storage-equip-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: result.Storage.Items[0].ID, ExpectedRevision: result.Storage.Revision})
	if err != nil || equipmentAt(result.Inventory.Character, 1).ID != weapon.ID || len(result.Storage.Items) != 0 {
		t.Fatalf("equipment withdraw=%+v %v", result, err)
	}
}
