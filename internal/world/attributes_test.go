package world

import (
	"bytes"
	"context"
	"encoding/binary"
	"reflect"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type attributeTestRepository struct {
	domain.Repository
	allocate func(domain.AttributeRequest) (domain.AttributeResult, error)
}

func TestAttributeAllocationsDoNotCrossPendingCombatVitals(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "single"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			s, lives, sinks, _, _, _ := pendingVitalsFixture(t, domain.CombatNormal)
			calls := 0
			s.repo = attributeTestRepository{allocate: func(domain.AttributeRequest) (domain.AttributeResult, error) {
				calls++
				return domain.AttributeResult{}, domain.ErrForbidden
			}}
			// Both the actor and another frozen party recipient must be held.
			for i, live := range lives {
				before := live.session.Character
				dirty := live.dirty
				packets := len(sinks[i].packets)
				var wire []byte
				if automatic {
					wire = make([]byte, 32)
					binary.LittleEndian.PutUint32(wire, 72)
					binary.LittleEndian.PutUint32(wire[4:], 2)
					binary.LittleEndian.PutUint64(wire[8:], 0x40)
					binary.LittleEndian.PutUint32(wire[16:], 3)
					binary.LittleEndian.PutUint64(wire[20:], 0x80)
					binary.LittleEndian.PutUint32(wire[28:], 2)
					if err := s.handleAttributeAutoAllocation(context.Background(), live, sinks[i], protocol.NewReader(wire)); err != nil {
						t.Fatal(err)
					}
				} else {
					wire = make([]byte, 12)
					binary.LittleEndian.PutUint32(wire, 72)
					binary.LittleEndian.PutUint64(wire[4:], 0x40)
					if err := s.handleAttributeAllocation(context.Background(), live, sinks[i], protocol.NewReader(wire)); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 0 || !reflect.DeepEqual(before, live.session.Character) || live.dirty != dirty {
					t.Fatal("pending receipt allowed AP/base/vitals mutation or repository call", i, calls)
				}
				if len(sinks[i].packets) != packets+1 || !bytes.Equal(sinks[i].packets[packets], protocol.EnableActions()) {
					t.Fatal("pending AP rejection did not unlock actions", i)
				}
			}
			if len(s.gameplay.pendingCombat) != 1 {
				t.Fatal("AP rejection changed unresolved combat")
			}
		})
	}
}

func (r attributeTestRepository) AllocateAttribute(_ context.Context, q domain.AttributeRequest) (domain.AttributeResult, error) {
	return r.allocate(q)
}

func TestAutoAttributeAllocationPublishesCommittedStatsAndUnlocksFailure(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "reject"}[rejected], func(t *testing.T) {
			live := &liveCharacter{session: domain.CharacterSession{CharacterID: 10, SessionID: "auto-ap", Generation: 2, Character: domain.Character{ID: 10, HP: 50, MaxHP: 50, STR: 4, DEX: 4, INT: 4, LUK: 4, AP: 5, Revision: 1}}}
			s := &Server{online: map[int64]domain.CharacterSession{}}
			s.repo = attributeTestRepository{allocate: func(q domain.AttributeRequest) (domain.AttributeResult, error) {
				if q.ClientTick != 19 || q.Session.CharacterID != 10 || q.Stat != 0 || len(q.Allocations) != 2 || q.Allocations[0].Amount != 3 || q.Allocations[1].Amount != 2 {
					t.Fatalf("lost native batch or owner: %+v", q)
				}
				if rejected {
					return domain.AttributeResult{}, domain.ErrInsufficient
				}
				c := q.Session.Character
				c.STR, c.DEX, c.AP, c.Revision = 7, 6, 0, 2
				return domain.AttributeResult{Character: c}, nil
			}}
			wire := make([]byte, 32)
			binary.LittleEndian.PutUint32(wire, 19)
			binary.LittleEndian.PutUint32(wire[4:], 2)
			binary.LittleEndian.PutUint64(wire[8:], 0x40)
			binary.LittleEndian.PutUint32(wire[16:], 3)
			binary.LittleEndian.PutUint64(wire[20:], 0x80)
			binary.LittleEndian.PutUint32(wire[28:], 2)
			sink := &gameplayTestSink{}
			if err := s.handleAttributeAutoAllocation(context.Background(), live, sink, protocol.NewReader(wire)); err != nil {
				t.Fatal(err)
			}
			if len(sink.packets) != 1 {
				t.Fatalf("expected one stats/enable-actions packet: %d", len(sink.packets))
			}
			if rejected {
				if string(sink.packets[0]) != string(protocol.EnableActions()) || live.session.Character.AP != 5 {
					t.Fatal("rejection changed stats or left UI locked")
				}
			} else if live.session.Character.STR != 7 || live.session.Character.DEX != 6 || live.session.Character.AP != 0 || string(sink.packets[0]) != string(protocol.AttributeStats(live.session.Character)) {
				t.Fatal("committed batch not reflected in live/client stats")
			}
		})
	}
}
