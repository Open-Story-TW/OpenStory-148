package world

import "testing"

func TestDropIdentifiersDoNotReuseOperationsAcrossWorldRestarts(t *testing.T) {
	a, b := newDropSpace(), newDropSpace()
	if a.nextOperationID("drop-meso", 42, 1) == b.nextOperationID("drop-meso", 42, 1) {
		t.Fatal("world restart reused a durable meso operation identity")
	}
	a.nextID.Store(999999)
	if a.AllocObjectID() != 999999 || a.AllocObjectID() != 0 {
		t.Fatal("meso object IDs overlap the NPC/mob range")
	}
}

func TestMesoPickupChecksAuthoritativePositionAndClaimsOnce(t *testing.T) {
	d := newDropSpace()
	d.RegisterMeso(1, 10000, 1, 100, 42, 0, 0, "drop-operation-one", "pickup-operation-one")
	if _, _, _, ok := d.ClaimMeso(1, 10000, 1, 151, 0); ok {
		t.Fatal("remote character claimed meso")
	}
	if _, _, _, ok := d.ClaimMeso(2, 10000, 1, 0, 0); ok {
		t.Fatal("other channel claimed meso")
	}
	if amount, _, _, ok := d.ClaimMeso(1, 10000, 1, 150, 0); !ok || amount != 100 {
		t.Fatal("nearby pickup rejected")
	}
	if _, _, _, ok := d.ClaimMeso(1, 10000, 1, 0, 0); ok {
		t.Fatal("meso claimed twice")
	}
}
