package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Potential scrolls and magnifying glasses are open unless
// MAPLEGO_ENABLE_POTENTIAL=0. Read-only native observation is independent.
func potentialCommandsEnabled() bool { return os.Getenv("MAPLEGO_ENABLE_POTENTIAL") != "0" }

func readPotentialCommand(op uint16, r *protocol.Reader) (domain.PotentialCommandRequest, error) {
	var q domain.PotentialCommandRequest
	switch op {
	case protocol.RecvUsePotentialScroll:
		v, err := protocol.ReadPotentialScrollInput(r)
		q.Action, q.ClientTick, q.SourceSlot, q.TargetSlot, q.LegendarySpirit = domain.PotentialSeal, v.ClientTick, v.SourceSlot, v.TargetSlot, v.LegendarySpirit
		return q, err
	case protocol.RecvUseMagnify:
		v, err := protocol.ReadMagnifyInput(r)
		q.Action, q.ClientTick, q.SourceSlot, q.TargetSlot = domain.PotentialMagnify, v.ClientTick, v.SourceSlot, v.TargetSlot
		return q, err
	default:
		return q, protocol.ErrMalformed
	}
}

func (s *Server) handlePotentialCommand(ctx context.Context, live *liveCharacter, sink packetSink, op uint16, r *protocol.Reader) error {
	q, err := readPotentialCommand(op, r)
	if err != nil {
		return err
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	reject := func() error { return s.writeScenePackets(visit, protocol.EnableActions()) }
	if !potentialCommandsEnabled() || s.gmBusy(live) {
		return reject()
	}
	repo, ok := s.repo.(domain.PotentialRepository)
	if !ok {
		return reject()
	}
	live.mu.Lock()
	q.Session = cloneCombatSession(live.session)
	live.mu.Unlock()
	q.Session.Lease = s.currentLease()
	if q.Session.Character.HP <= 0 {
		return reject()
	}
	// Verify this exact entry before SQL without retaining its gate over SQL.
	if err = s.writeScenePackets(visit); err != nil {
		return err
	}
	result, err := repo.ApplyPotential(ctx, q)
	resolved := false
	if err != nil && !combatKnownFailure(err) {
		// A timeout/Commit error never authorizes a second Apply/RNG call.
		verify, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var found bool
		result, found, err = repo.ResolvePotential(verify, q)
		cancel()
		if err == nil && !found {
			return reject() // Same SQL locks proved the first transaction ended.
		}
		if err != nil {
			return fmt.Errorf("%w: %v", domain.ErrPotentialOutcomeUnknown, err)
		}
		resolved = true
	}
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return err
		}
		return reject()
	}
	delta := !result.Replayed
	if resolved {
		// A lookup can also find an earlier duplicate whose commit was already
		// reflected in this live aggregate. It must not replay its old deltas.
		delta = result.CommittedRevision > q.Session.Character.Revision
	}
	if err = validatePotentialResult(q, result, delta); err != nil {
		return err
	}
	var packets [][]byte
	var effect []byte
	if delta {
		packet := protocol.ModifyInventory(result.Inventory.Changes)
		if packet == nil {
			return fmt.Errorf("potential receipt has no complete inventory rewrite")
		}
		packets = append(packets, packet)
		if result.MesoFee != 0 {
			packets = append(packets, protocol.UpdateMesos(result.Inventory.Character.Mesos, true))
		}
		// A maximum buff may expire while even a bag operation is in SQL.
		// Publish its final clamp rather than leaving client HP above MaxHP.
		before, after := q.Session.Character, result.Inventory.Character
		if q.TargetSlot < 0 || before.HP != after.HP || before.MP != after.MP || before.MaxHP != after.MaxHP || before.MaxMP != after.MaxMP {
			packets = append(packets, protocol.InventoryVitals(result.Inventory.Character))
		}
		if q.Action == domain.PotentialMagnify {
			packets = append(packets, protocol.MagnifySuccess(q.Session.CharacterID, q.TargetSlot))
		} else {
			effect = protocol.ScrollEffect(q.Session.CharacterID, domain.ScrollResult{Status: result.Status,
				ScrollID: result.ToolID, EquipmentTemplateID: result.EquipmentTemplateID, LegendarySpirit: q.LegendarySpirit})
			packets = append(packets, effect)
		}
	}
	packets = append(packets, protocol.EnableActions())
	for _, packet := range packets {
		if packet == nil {
			return fmt.Errorf("potential reply cannot be encoded")
		}
	}
	unlock, err := s.lockScenePublication(q.Session.CharacterID)
	if err != nil {
		return err
	}
	if !s.sceneCurrent(visit) {
		unlock()
		return domain.ErrStaleOwner
	}
	s.applyInventoryResult(live, result.Inventory.Character)
	for _, packet := range packets {
		if err = sink.WritePacket(packet); err != nil {
			unlock()
			return err
		}
	}
	unlock()
	if delta {
		slog.Info("potential_command_committed", "character", q.Session.CharacterID,
			"world", q.Session.Lease.WorldID, "channel", q.Session.ChannelID, "map", q.Session.Character.MapID,
			"client_tick", q.ClientTick, "action", q.Action, "source_slot", q.SourceSlot, "target_slot", q.TargetSlot,
			"tool_id", result.ToolID, "tool_instance_id", result.ToolInstanceID, "equipment_id", result.EquipmentID,
			"status", result.Status, "meso_fee", result.MesoFee, "plan_sha256", result.PlanSHA256,
			"revision", result.CommittedRevision, "same_operation_resolved", resolved)
		if q.Action == domain.PotentialSeal {
			s.broadcastPotentialEffect(visit, effect)
		}
	}
	return nil
}

func validatePotentialResult(q domain.PotentialCommandRequest, r domain.PotentialCommandResult, delta bool) error {
	c := r.Inventory.Character
	if !delta && c.Revision != q.Session.Character.Revision {
		// A later aggregate cannot be silently installed with only EnableActions.
		// Its intervening inventory changes require a fresh connection snapshot.
		return domain.ErrStaleOwner
	}
	if r.Action != q.Action || r.SourceSlot != q.SourceSlot || r.TargetSlot != q.TargetSlot || r.LegendarySpirit != q.LegendarySpirit ||
		r.Status < 0 || r.Status > 1 || r.MesoFee < 0 || r.ToolInstanceID <= 0 || r.EquipmentID <= 0 || r.EquipmentTemplateID < 1000000 ||
		c.ID != q.Session.CharacterID || c.AccountID != q.Session.AccountID || c.WorldID != q.Session.Lease.WorldID || c.MapID != q.Session.Character.MapID ||
		r.CommittedRevision <= 0 || c.Revision < r.CommittedRevision || c.Revision < q.Session.Character.Revision || delta && c.Revision != r.CommittedRevision {
		return fmt.Errorf("potential receipt does not match the exact request/owner/revision")
	}
	hash := sha256.Sum256(r.PlanJSON)
	var plan content.PotentialPlan
	if hex.EncodeToString(hash[:]) != r.PlanSHA256 || json.Unmarshal(r.PlanJSON, &plan) != nil ||
		plan.Operation != string(q.Action) || plan.ToolID != r.ToolID || int64(plan.MesoFee) != r.MesoFee || plan.ToolCount != 1 ||
		plan.Before != r.LogicalPolicyBefore || plan.ScrollSuccess != (r.Status == 1) || r.BeforeStored != r.EquipmentBefore.Potential {
		return fmt.Errorf("potential receipt transcript is incomplete or inconsistent")
	}
	if q.Action == domain.PotentialMagnify && r.Status != 1 {
		return fmt.Errorf("magnifier failure cannot produce a committed success reply")
	}
	if delta {
		if r.Inventory.Drop != nil || len(r.Inventory.Changes) != 3 || r.Inventory.Changes[1] != (domain.InventoryChange{Mode: 3, InvType: 1, Slot: q.TargetSlot}) {
			return fmt.Errorf("potential receipt has an unrelated or incomplete inventory batch")
		}
		if r.CommittedRevision <= q.Session.Character.Revision {
			return fmt.Errorf("potential mutation did not advance the asset revision")
		}
		if c.Mesos != q.Session.Character.Mesos-r.MesoFee {
			return fmt.Errorf("potential fee and actual wallet difference disagree")
		}
		beforeFound, sourceFound := false, false
		for _, e := range q.Session.Character.Equipment {
			if e.Slot == q.TargetSlot && e == r.EquipmentBefore && e.ID == r.EquipmentID && e.ItemID == r.EquipmentTemplateID {
				beforeFound = true
			}
		}
		for _, source := range q.Session.Character.Items {
			if source.InvType != 2 || source.Slot != q.SourceSlot || source.ID != r.ToolInstanceID || source.TemplateID != r.ToolID || source.Quantity < 1 {
				continue
			}
			left, matches := source.Quantity-1, 0
			material := domain.InventoryChange{Mode: 3, InvType: 2, Slot: q.SourceSlot}
			if left > 0 {
				material.Mode, material.Quantity = 1, left
			}
			if r.Inventory.Changes[0] != material {
				return fmt.Errorf("potential material packet disagrees with committed quantity")
			}
			for _, item := range c.Items {
				if item.ID == source.ID {
					if left == 0 || item.InvType != source.InvType || item.Slot != source.Slot || item.TemplateID != source.TemplateID || item.Quantity != left {
						return fmt.Errorf("potential source material did not decrease exactly once")
					}
					matches++
				}
			}
			if left > 0 && matches != 1 || left == 0 && matches != 0 {
				return fmt.Errorf("potential source material identity was lost or duplicated")
			}
			sourceFound = true
		}
		if !beforeFound || !sourceFound {
			return fmt.Errorf("potential receipt does not bind the actual source/target instances")
		}
		found := false
		for _, e := range c.Equipment {
			if e.ID == r.EquipmentID && e.ItemID == r.EquipmentTemplateID && e.Slot == q.TargetSlot && e.Potential == plan.After {
				unchanged := e
				unchanged.Potential = r.EquipmentBefore.Potential
				if unchanged != r.EquipmentBefore {
					return fmt.Errorf("potential command changed unrelated equipment fields")
				}
				add := r.Inventory.Changes[2]
				wireEquipment := add.Equipment
				add.Equipment = nil
				if add != (domain.InventoryChange{Mode: 0, InvType: 1, Slot: q.TargetSlot}) || wireEquipment == nil || *wireEquipment != e {
					return fmt.Errorf("potential equipment packet disagrees with committed equipment")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("potential receipt lost the actual rewritten equipment")
		}
	}
	return nil
}

// No global registry/map lock crosses socket IO, and no actor CID gate is held
// while acquiring another player's gate. Old entries never receive the effect
// after leaving/re-entering even if they reuse the same map and sink.
func (s *Server) broadcastPotentialEffect(actor *sceneVisit, packet []byte) {
	var peers []*sceneVisit
	s.mu.RLock()
	for _, peer := range s.scenes {
		if sameSceneOwner(peer.owner, actor.owner) || peer.owner.Lease.WorldID != actor.owner.Lease.WorldID ||
			peer.owner.ChannelID != actor.owner.ChannelID || peer.owner.Character.MapID != actor.owner.Character.MapID {
			continue
		}
		peers = append(peers, peer)
	}
	s.mu.RUnlock()
	for _, peer := range peers {
		_ = s.writeScenePackets(peer, packet)
	}
}
