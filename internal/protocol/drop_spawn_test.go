package protocol

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestMonsterDropInitialAnimationMatchesJavaLayout(t *testing.T) {
	// CField.dropItemFromMapObject: mode1, destination, reserved I32,
	// source point, delay U16, item-only expiration, non-player flag U16.
	// Source/destination are the current durable combat-loot kill position.
	item := domain.ItemDrop{ObjectID: 1000000000, OwnerID: 42, TemplateID: 2000000, X: -192, Y: 210}
	before := item
	want, _ := hex.DecodeString("e1010100ca9a3b0080841e002a0000000240ffd2000000000040ffd2000000008005bb46e617020100")
	if got := DropMonsterLoot(item); !bytes.Equal(got, want) || !reflect.DeepEqual(item, before) {
		t.Fatalf("initial item drop got %x want %x", got, want)
	}
	meso := item
	meso.TemplateID, meso.Mesos = 0, 123
	want, _ = hex.DecodeString("e1010100ca9a3b017b0000002a0000000240ffd2000000000040ffd20000000100")
	if got := DropMonsterLoot(meso); !bytes.Equal(got, want) {
		t.Fatalf("initial meso drop got %x want %x", got, want)
	}
	// Existing reentry/item/player-currency encoders retain mode2 and length.
	if got := DropInventoryItem(item); len(got) != 35 || got[2] != 2 || got[len(got)-1] != 0 {
		t.Fatalf("stationary item changed: %x", got)
	}
	meso.PlayerDrop = true
	if got := DropInventoryItem(meso); len(got) != 27 || got[2] != 2 || got[len(got)-2] != 0 {
		t.Fatalf("player meso changed: %x", got)
	}
	if DropMonsterLoot(meso) != nil {
		t.Fatal("player drop admitted as natural monster animation")
	}
}

func TestMonsterDropAnimationKeepsQuestOwnershipAndBoundaryValidation(t *testing.T) {
	drop := domain.ItemDrop{ObjectID: 1000000000, OwnerID: 42, TemplateID: 4031802, QuestID: 1035, QuestDropType: 1, QuestOwnerUntil: time.Now().Add(time.Hour).UnixMilli(), X: -32768, Y: 32767}
	packet := DropMonsterLoot(drop)
	if len(packet) != 41 || packet[16] != 1 {
		t.Fatal("quest protection lost", packet)
	}
	drop.QuestOwnerUntil = 1
	if got := DropMonsterLoot(drop); got[16] != 2 || drop.QuestDropType != 1 {
		t.Fatal("expired protection or audit changed", got)
	}
	for _, mutate := range []func(*domain.ItemDrop){
		func(d *domain.ItemDrop) { d.X = -32769 },
		func(d *domain.ItemDrop) { d.Y = 32768 },
		func(d *domain.ItemDrop) { d.ObjectID = 0 },
		func(d *domain.ItemDrop) { d.OwnerID = 0 },
		func(d *domain.ItemDrop) { d.TemplateID = 0 },
		func(d *domain.ItemDrop) { d.Mesos = 2147483648 },
		func(d *domain.ItemDrop) { d.Mesos = 100 }, // mixed item/meso is invalid
	} {
		bad := drop
		mutate(&bad)
		if DropMonsterLoot(bad) != nil {
			t.Fatal("invalid animation emitted", bad)
		}
	}
}
