package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type potentialWorldRepo struct {
	domain.Repository
	apply   func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, error)
	resolve func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error)
}

func (r *potentialWorldRepo) ApplyPotential(_ context.Context, q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
	return r.apply(q)
}
func (r *potentialWorldRepo) ResolvePotential(_ context.Context, q domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error) {
	return r.resolve(q)
}

func potentialWorldFixture(t *testing.T, seal bool) (*Server, *liveCharacter, *gameplayTestSink, *potentialWorldRepo) {
	t.Helper()
	t.Setenv("MAPLEGO_ENABLE_POTENTIAL", "1")
	s, live, sink, _ := gmWorldFixture(t)
	live.session.Character.Mesos = 5000
	live.session.Character.Equipment = []domain.Equipment{{ID: 111, ItemID: 1322005, Slot: 1, Potential: domain.EquipmentPotential{Present: true, RawState: 1, Options: [5]uint16{65519}, Flags: 65535}}}
	live.session.Character.Items = []domain.InventoryItem{{ID: 222, TemplateID: 2460003, InvType: 2, Slot: 2, Quantity: 2}}
	if seal {
		live.session.Character.Equipment[0].Potential = domain.EquipmentPotential{}
		live.session.Character.Items[0].TemplateID = 2049700
	}
	repo := &potentialWorldRepo{}
	s.repo = repo
	sink.packets = nil
	return s, live, sink, repo
}

func potentialWire(seal bool) (uint16, *protocol.Reader) {
	w := protocol.NewRawWriter()
	w.U32(99)
	w.I16(2)
	w.I16(1)
	if seal {
		w.U8(0)
		return protocol.RecvUsePotentialScroll, protocol.NewReader(w.Packet())
	}
	return protocol.RecvUseMagnify, protocol.NewReader(w.Packet())
}

func potentialWorldResult(q domain.PotentialCommandRequest) domain.PotentialCommandResult {
	c := cloneCombatSession(q.Session).Character
	before := c.Equipment[0]
	source := c.Items[0]
	after := domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10001, 10002}, Flags: 65535}
	fee := 1000
	if q.Action == domain.PotentialSeal {
		fee = 0
		after = domain.EquipmentPotential{Present: true}
	}
	c.Mesos -= int64(fee)
	c.Revision++
	c.Equipment[0].Potential = after
	c.Items[0].Quantity--
	material := domain.InventoryChange{Mode: 1, InvType: 2, Slot: 2, Quantity: c.Items[0].Quantity}
	if c.Items[0].Quantity == 0 {
		c.Items = c.Items[1:]
		material.Mode = 3
	}
	plan := content.PotentialPlan{Operation: string(q.Action), Before: before.Potential, After: after, ToolID: source.TemplateID, ToolCount: 1, MesoFee: fee, ScrollSuccess: q.Action == domain.PotentialMagnify}
	raw, _ := json.Marshal(plan)
	h := sha256.Sum256(raw)
	status := 0
	if plan.ScrollSuccess {
		status = 1
	}
	return domain.PotentialCommandResult{Action: q.Action, OperationID: "unit-receipt", SourceSlot: q.SourceSlot, TargetSlot: q.TargetSlot, ToolID: source.TemplateID, ToolInstanceID: 222,
		EquipmentID: before.ID, EquipmentTemplateID: before.ItemID, Status: status, MesoFee: int64(fee), CommittedRevision: c.Revision, EquipmentBefore: before,
		BeforeStored: before.Potential, NativeBefore: before.Potential, LogicalPolicyBefore: before.Potential, PlanJSON: raw, PlanSHA256: hex.EncodeToString(h[:]),
		Inventory: domain.InventoryResult{Character: c, Changes: []domain.InventoryChange{material, {Mode: 3, InvType: 1, Slot: 1}, {Mode: 0, InvType: 1, Slot: 1, Equipment: &c.Equipment[0]}}}}
}

func TestPotentialControllerExhaustedMaterialUsesRemovalBatch(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	live.session.Character.Items[0].Quantity = 1
	repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		return potentialWorldResult(q), nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil || len(live.session.Character.Items) != 0 || sink.count(protocol.SendModifyInventory) != 1 {
		t.Fatal("last magnifier was not removed exactly once", err)
	}
	v := protocol.NewReader(sink.packets[0])
	if v.U16() != protocol.SendModifyInventory || v.U8() != 1 || v.U8() != 3 || v.U8() != 0 || v.U8() != 3 || v.U8() != 2 || v.I16() != 2 {
		t.Fatal("exhausted material packet is not a USE removal")
	}
}

func TestPotentialControllerClosedGateDoesNotCallRepository(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	t.Setenv("MAPLEGO_ENABLE_POTENTIAL", "0")
	repo.apply = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		t.Fatal("closed capability spent resources")
		return domain.PotentialCommandResult{}, nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil || sink.count(protocol.SendModifyInventory) != 0 || live.session.Character.Mesos != 5000 {
		t.Fatal("closed gate mutated inventory", err)
	}
}

func TestPotentialControllerUnknownCommitResolvesSameRequestOnce(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	var original domain.PotentialCommandRequest
	apply, resolve := 0, 0
	repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		apply++
		original = q
		return domain.PotentialCommandResult{}, domain.ErrPotentialOutcomeUnknown
	}
	repo.resolve = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error) {
		resolve++
		if !reflect.DeepEqual(q, original) {
			t.Fatal("resolver reconstructed another operation")
		}
		if live.session.Character.Mesos != 5000 || sink.count(protocol.SendModifyInventory) != 0 {
			t.Fatal("unconfirmed result was applied")
		}
		return potentialWorldResult(q), true, nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil {
		t.Fatal(err)
	}
	if apply != 1 || resolve != 1 || live.session.Character.Mesos != 4000 || live.session.Character.Items[0].Quantity != 1 || sink.count(protocol.SendModifyInventory) != 1 || sink.count(protocol.SendMagnifyEffect) != 1 {
		t.Fatal("resolved receipt was not published exactly once")
	}
	inv := protocol.NewReader(sink.packets[0])
	if inv.U16() != protocol.SendModifyInventory || inv.U8() != 1 || inv.U8() != 3 || inv.U8() != 0 || inv.U8() != 1 || inv.U8() != 2 || inv.I16() != 2 || inv.I16() != 1 {
		t.Fatal("actual three-change material-first batch differs from native contract")
	}
}

func TestPotentialControllerUncertainOrRolledBackOutcomeNeverPublishesSuccess(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown", false: "rolledback"}[unknown], func(t *testing.T) {
			s, live, sink, repo := potentialWorldFixture(t, false)
			repo.apply = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
				return domain.PotentialCommandResult{}, domain.ErrPotentialOutcomeUnknown
			}
			repo.resolve = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error) {
				if unknown {
					return domain.PotentialCommandResult{}, false, errors.New("lookup unavailable")
				}
				return domain.PotentialCommandResult{}, false, nil
			}
			op, r := potentialWire(false)
			err := s.handlePotentialCommand(context.Background(), live, sink, op, r)
			if unknown && !errors.Is(err, domain.ErrPotentialOutcomeUnknown) || !unknown && err != nil || live.session.Character.Mesos != 5000 || sink.count(protocol.SendModifyInventory) != 0 || sink.count(protocol.SendMagnifyEffect) != 0 {
				t.Fatal("uncertain/rolled back transaction fabricated success", err)
			}
		})
	}
}

func TestPotentialControllerRejectsCorruptReceiptAndReplacedVisit(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{true: "same-map-reentry", false: "corrupt-plan"}[replace], func(t *testing.T) {
			s, live, sink, repo := potentialWorldFixture(t, false)
			repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
				out := potentialWorldResult(q)
				if replace {
					seedBoundScene(t, s, live, sink)
				} else {
					out.PlanJSON = append(out.PlanJSON, ' ')
				}
				return out, nil
			}
			op, r := potentialWire(false)
			if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err == nil || sink.count(protocol.SendModifyInventory) != 0 || live.session.Character.Mesos != 5000 {
				t.Fatal("invalid/stale result crossed client boundary", err)
			}
		})
	}
}

func TestPotentialControllerReplayDoesNotSpendOrReplayInventoryDelta(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	q := domain.PotentialCommandRequest{Session: cloneCombatSession(live.session), Action: domain.PotentialMagnify, ClientTick: 99, SourceSlot: 2, TargetSlot: 1}
	result := potentialWorldResult(q)
	live.session.Character = result.Inventory.Character
	result.Replayed = true
	result.Inventory.Changes = nil
	repo.apply = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) { return result, nil }
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil || sink.count(protocol.SendModifyInventory) != 0 || sink.count(protocol.SendMagnifyEffect) != 0 || live.session.Character.Mesos != 4000 {
		t.Fatal("replay duplicated material or effect", err)
	}
}

func TestPotentialControllerResolvedEarlierDuplicateDoesNotReplayDelta(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	q := domain.PotentialCommandRequest{Session: cloneCombatSession(live.session), Action: domain.PotentialMagnify, ClientTick: 99, SourceSlot: 2, TargetSlot: 1}
	result := potentialWorldResult(q)
	live.session.Character = result.Inventory.Character
	repo.apply = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		return domain.PotentialCommandResult{}, domain.ErrPotentialOutcomeUnknown
	}
	repo.resolve = func(domain.PotentialCommandRequest) (domain.PotentialCommandResult, bool, error) {
		return result, true, nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil || sink.count(protocol.SendModifyInventory) != 0 || sink.count(protocol.SendMagnifyEffect) != 0 || live.session.Character.Mesos != 4000 {
		t.Fatal("read-only resolution repeated an already applied delta", err)
	}
}

func TestPotentialControllerFutureReplaySnapshotRequiresReconnect(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		r := potentialWorldResult(q)
		r.Replayed = true
		r.Inventory.Changes = nil
		return r, nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); !errors.Is(err, domain.ErrStaleOwner) || len(sink.packets) != 0 || live.session.Character.Mesos != 5000 || live.session.Character.Items[0].Quantity != 2 {
		t.Fatal("future replay snapshot silently replaced live assets", err)
	}
}

func TestPotentialControllerRejectsBatchThatDisagreesWithCommittedAssets(t *testing.T) {
	for _, which := range []string{"material-quantity", "equipment-stats", "equipment-potential", "order", "extra", "remove-destination", "foreign-item"} {
		t.Run(which, func(t *testing.T) {
			s, live, sink, repo := potentialWorldFixture(t, false)
			repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
				r := potentialWorldResult(q)
				switch which {
				case "material-quantity":
					r.Inventory.Changes[0].Quantity = 99
				case "equipment-stats", "equipment-potential":
					e := *r.Inventory.Changes[2].Equipment
					if which == "equipment-stats" {
						e.WATK++
					} else {
						e.Potential.Options[0]++
					}
					r.Inventory.Changes[2].Equipment = &e
				case "order":
					r.Inventory.Changes[0], r.Inventory.Changes[1] = r.Inventory.Changes[1], r.Inventory.Changes[0]
				case "extra":
					r.Inventory.Changes = append(r.Inventory.Changes, r.Inventory.Changes[0])
				case "remove-destination":
					r.Inventory.Changes[1].Destination = 2
				case "foreign-item":
					r.Inventory.Changes[2].Item = &domain.InventoryItem{ID: 333, InvType: 2, Slot: 3, TemplateID: 2000000, Quantity: 1}
				}
				return r, nil
			}
			op, r := potentialWire(false)
			if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err == nil || len(sink.packets) != 0 || live.session.Character.Mesos != 5000 {
				t.Fatal("published a batch which disagrees with committed assets", err)
			}
		})
	}
}

func TestPotentialControllerRejectsWrongMaterialAndUnrelatedEquipmentMutation(t *testing.T) {
	for which := 0; which < 4; which++ {
		s, live, sink, repo := potentialWorldFixture(t, false)
		repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
			r := potentialWorldResult(q)
			switch which {
			case 0:
				r.Inventory.Character.Items[0].Quantity = 2
			case 1:
				r.ToolInstanceID++
			case 2:
				r.EquipmentBefore.ID++
			case 3:
				r.Inventory.Character.Equipment[0].WATK++
			}
			return r, nil
		}
		op, r := potentialWire(false)
		if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err == nil || sink.count(protocol.SendModifyInventory) != 0 || live.session.Character.Mesos != 5000 {
			t.Fatal("backend display fields substituted for a real bound mutation", which, err)
		}
	}
}

func TestPotentialControllerFailedEpicStillPublishesFailureAndCommittedMaterial(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, true)
	peerSink := &gameplayTestSink{}
	peer := cloneCombatSession(live.session)
	peer.CharacterID = 44
	peer.Character.ID = 44
	peer.AccountID = 33
	peer.Character.AccountID = 33
	peer.SessionID = "peer"
	peerLive := &liveCharacter{session: peer}
	seedBoundScene(t, s, peerLive, peerSink)
	peerSink.packets = nil
	repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		return potentialWorldResult(q), nil
	}
	op, r := potentialWire(true)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil {
		t.Fatal(err)
	}
	if sink.count(protocol.SendScrollEffect) != 1 || peerSink.count(protocol.SendScrollEffect) != 1 || sink.count(protocol.SendMagnifyEffect) != 0 || live.session.Character.Items[0].Quantity != 1 || live.session.Character.Mesos != 5000 {
		t.Fatal("epic failure was refunded, duplicated or shown as magnify success")
	}
}

func TestPotentialControllerBagOperationPublishesBuffExpiryClamp(t *testing.T) {
	s, live, sink, repo := potentialWorldFixture(t, false)
	live.session.Character.VitalsModel = 1
	live.session.Character.BaseMaxHP, live.session.Character.BaseMaxMP = 100, 100
	live.session.Character.HP, live.session.Character.MaxHP = 150, 160
	live.session.Character.MP, live.session.Character.MaxMP = 150, 160
	repo.apply = func(q domain.PotentialCommandRequest) (domain.PotentialCommandResult, error) {
		r := potentialWorldResult(q)
		r.Inventory.Character.HP, r.Inventory.Character.MaxHP = 100, 100
		r.Inventory.Character.MP, r.Inventory.Character.MaxMP = 100, 100
		return r, nil
	}
	op, r := potentialWire(false)
	if err := s.handlePotentialCommand(context.Background(), live, sink, op, r); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, packet := range sink.packets {
		v := protocol.NewReader(packet)
		if v.U16() != protocol.SendUpdateStats || v.U8() != 1 || v.I64() != 0x3c00 {
			continue
		}
		for i := 0; i < 4; i++ {
			if v.I32() != 100 {
				t.Fatal("bag operation published pre-expiry vitals")
			}
		}
		matched++
	}
	if matched != 1 || live.session.Character.HP != 100 || live.session.Character.MaxHP != 100 {
		t.Fatal("bag operation lost the committed clamp", matched)
	}
}
