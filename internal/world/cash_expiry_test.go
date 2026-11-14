package world

import (
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

type fieldExpiryRepo struct {
	domain.Repository
	result domain.InventoryResult
	err    error
}

func (r fieldExpiryRepo) ExpireCashInventory(context.Context, domain.CharacterSession) (domain.InventoryResult, error) {
	return r.result, r.err
}

func TestFieldExpiryPreflightPreservesLivePositionAndClampsBeforeSnapshot(t *testing.T) {
	live := &liveCharacter{dirty: true, session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, X: 77, Y: 88, MapID: 20000, HP: 65, MaxHP: 70, BaseMaxHP: 50, VitalsModel: 1, Revision: 2, Equipment: []domain.Equipment{{ID: 9, CashID: 10, Slot: -101, HP: 20}}}}}
	clean := live.session.Character
	clean.Equipment = nil
	clean.HP = 50
	clean.MaxHP = 50
	clean.Revision = 3
	clean.X = 0
	clean.Y = 0
	clean.MapID = 10000
	s := &Server{online: map[int64]domain.CharacterSession{}, repo: fieldExpiryRepo{result: domain.InventoryResult{Character: clean, Changes: []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: -101}}}}}
	if err := s.expireCashBeforeField(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	c := live.session.Character
	if c.HP != 50 || c.MaxHP != 50 || c.BaseMaxHP != 50 || len(c.Equipment) != 0 || c.X != 77 || c.Y != 88 || c.MapID != 20000 || !live.dirty {
		t.Fatalf("initial snapshot data %+v", c)
	}
	s.repo = fieldExpiryRepo{err: errors.New("expiry commit failed")}
	if err := s.expireCashBeforeField(context.Background(), live); err == nil {
		t.Fatal("expiry failure ignored")
	}
	if live.session.Character.Revision != 3 {
		t.Fatal("failed expiry mutated live state")
	}
}
