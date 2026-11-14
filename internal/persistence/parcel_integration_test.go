package persistence

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestParcelSendTakeDeleteAndExpire(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	previous := domain.ParcelDelay()
	domain.SetParcelDelay(0)
	t.Cleanup(func() { domain.SetParcelDelay(previous) })
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=200000 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a.Character.Mesos = 200000

	// Unknown name, own account and an empty parcel are refused before anything moves.
	if _, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-none", Mesos: 10, Recipient: "NoSuchName9"}); !errors.Is(err, domain.ErrParcelRecipient) {
		t.Fatalf("unknown recipient=%v", err)
	}
	if _, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-self", Mesos: 10, Recipient: a.Character.Name}); !errors.Is(err, domain.ErrParcelSameAccount) {
		t.Fatalf("same account=%v", err)
	}
	if _, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-empty", Recipient: b.Character.Name}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty=%v", err)
	}
	if _, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-broke", Mesos: 199000, Recipient: b.Character.Name}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("not enough mesos=%v", err)
	}

	// Two potions and 100,000 mesos: fee 800 + 5,000.
	sent, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-send", InvType: 2, Slot: 1, Quantity: 2, Mesos: 100000, Recipient: b.Character.Name})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Cost != 105800 || sent.Inventory.Character.Mesos != 200000-105800 || sent.Parcel.Item == nil || sent.Parcel.Item.Quantity != 2 || sent.Parcel.ReceiverID != b.CharacterID {
		t.Fatalf("send=%+v", sent)
	}
	a.Character = sent.Inventory.Character
	if a.Character.Items[0].Quantity != 3 {
		t.Fatalf("sender kept %+v", a.Character.Items)
	}

	// An equipped weapon moved to the bag goes as itself.
	weapon := equipmentAt(a.Character, -11)
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 1, InvType: 1, Source: -11, Destination: 1, Quantity: -1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = moved.Character
	equip, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-equip", InvType: 1, Slot: 1, Quantity: 1, Recipient: b.Character.Name})
	if err != nil || equip.Parcel.Equipment == nil || equip.Parcel.Equipment.ID != weapon.ID || equipmentAt(equip.Inventory.Character, 1).ID != 0 {
		t.Fatalf("equip send=%+v %v", equip, err)
	}
	a.Character = equip.Inventory.Character

	box, err := s.OpenParcels(ctx, b)
	if err != nil || len(box.Parcels) != 2 || box.Parcels[0].SenderName != a.Character.Name || box.Parcels[0].Mesos != 100000 {
		t.Fatalf("box=%+v %v", box, err)
	}
	other, err := s.OpenParcels(ctx, a)
	if err != nil || len(other.Parcels) != 0 {
		t.Fatalf("sender box=%+v %v", other, err)
	}
	// Someone else's parcel id is not found.
	if _, err = s.TakeParcel(ctx, domain.ParcelTake{Session: a, OperationID: "parcel-test-steal", ParcelID: sent.Parcel.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("steal=%v", err)
	}

	taken, err := s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-test-take", ParcelID: sent.Parcel.ID})
	if err != nil || taken.Inventory.Character.Mesos != b.Character.Mesos+100000 {
		t.Fatalf("take=%+v %v", taken, err)
	}
	potions := 0
	for _, item := range taken.Inventory.Character.Items {
		if item.TemplateID == 2000000 {
			potions += item.Quantity
		}
	}
	if potions != 7 {
		t.Fatalf("receiver potions %d", potions)
	}
	b.Character = taken.Inventory.Character
	got, err := s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-test-take-equip", ParcelID: equip.Parcel.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range got.Inventory.Character.Equipment {
		found = found || (e.ID == weapon.ID && e.Slot > 0)
	}
	if !found {
		t.Fatalf("weapon not received %+v", got.Inventory.Character.Equipment)
	}
	b.Character = got.Inventory.Character
	if _, err = s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-test-again", ParcelID: sent.Parcel.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("taken twice=%v", err)
	}

	// Delete throws a parcel away; an expired one shows once on opening.
	gone, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-gone", Mesos: 1, Recipient: b.Character.Name})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = gone.Inventory.Character
	if _, err = s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-test-delete", ParcelID: gone.Parcel.ID, Delete: true}); err != nil {
		t.Fatal(err)
	}
	old, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-old", Mesos: 2, Recipient: b.Character.Name})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = old.Inventory.Character
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_parcels SET sent_at=sent_at-interval '31 days',arrives_at=arrives_at-interval '31 days',expires_at=expires_at-interval '31 days' WHERE id=$1`, old.Parcel.ID); err != nil {
		t.Fatal(err)
	}
	box, err = s.OpenParcels(ctx, b)
	if err != nil || len(box.Parcels) != 0 || len(box.Expired) != 1 || box.Expired[0].ID != old.Parcel.ID {
		t.Fatalf("expired box=%+v %v", box, err)
	}
	if box, err = s.OpenParcels(ctx, b); err != nil || len(box.Expired) != 0 {
		t.Fatalf("expired twice=%+v %v", box, err)
	}

	// The box holds ten.
	for i := 0; i < domain.ParcelCapacity; i++ {
		r, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-fill-" + string(rune('a'+i)), Mesos: 1, Recipient: b.Character.Name})
		if err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
		a.Character = r.Inventory.Character
	}
	if _, err = s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-test-full", Mesos: 1, Recipient: b.Character.Name}); !errors.Is(err, domain.ErrParcelBoxFull) {
		t.Fatalf("full=%v", err)
	}
	if quick, err := s.HasQuickParcel(ctx, b); err != nil || quick {
		t.Fatalf("quick=%v %v", quick, err)
	}
}

func TestParcelInTransitUntilItArrives(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	previous := domain.ParcelDelay()
	domain.SetParcelDelay(time.Hour)
	t.Cleanup(func() { domain.SetParcelDelay(previous) })
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=100000 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	sent, err := s.SendParcel(ctx, domain.ParcelSend{Session: a, OperationID: "parcel-transit-send", Mesos: 10, Recipient: b.Character.Name})
	if err != nil {
		t.Fatal(err)
	}
	if got := sent.Parcel.ArrivesAt.Sub(sent.Parcel.SentAt); got < time.Hour-time.Second || got > time.Hour+time.Second {
		t.Fatalf("arrives after %v", got)
	}
	if got := sent.Parcel.ExpiresAt.Sub(sent.Parcel.ArrivesAt); got != domain.ParcelLifetime {
		t.Fatalf("lifetime %v", got)
	}
	box, err := s.OpenParcels(ctx, b)
	if err != nil || len(box.Parcels) != 1 {
		t.Fatalf("box=%+v %v", box, err)
	}
	if _, err = s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-transit-early", ParcelID: sent.Parcel.ID}); !errors.Is(err, domain.ErrParcelInTransit) {
		t.Fatalf("early take=%v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_parcels SET sent_at=sent_at-interval '2 hours',arrives_at=arrives_at-interval '2 hours',expires_at=expires_at-interval '2 hours' WHERE id=$1`, sent.Parcel.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.TakeParcel(ctx, domain.ParcelTake{Session: b, OperationID: "parcel-transit-take", ParcelID: sent.Parcel.ID}); err != nil || got.Inventory.Character.Mesos != b.Character.Mesos+10 {
		t.Fatalf("take after arrival=%+v %v", got, err)
	}
}
