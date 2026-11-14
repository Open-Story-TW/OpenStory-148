package world

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func pickupGainNotification(result domain.InventoryResult) []byte {
	if result.Replayed || result.Drop == nil {
		return nil
	}
	if result.Drop.Mesos > 0 {
		return protocol.ShowMesoGain(result.Drop.Mesos)
	}
	return protocol.ShowItemGain(result.Drop.TemplateID, result.Drop.Quantity)
}

// Called under the same recipient persistence lock and exact-visit checks as
// combat stat publication. Outcome lookup marks receipts Replayed too, so the
// revision transition (not that flag) determines whether this visit is applying
// new progress. A late/old receipt must not show an old reward again.
func publishCombatGainNotifications(t *combatTicket, p combatLivePeer, current domain.Character, result domain.CombatAttackResult) {
	if t.rewardNotified[p.id] {
		return
	}
	var committed *domain.Character
	for i := range result.Characters {
		if result.Characters[i].ID == p.id {
			committed = &result.Characters[i]
			break
		}
	}
	if committed == nil || committed.Revision <= p.session.Character.Revision || current.Revision != committed.Revision {
		return
	}
	if t.rewardNotified == nil {
		t.rewardNotified = make(map[int64]bool)
	}
	// A write can fail after delivery; never retry a notification on that ticket.
	t.rewardNotified[p.id] = true
	for _, spawn := range result.Spawns {
		for _, grant := range spawn.Grants {
			if grant.CharacterID != p.id {
				continue
			}
			if packet := protocol.ShowExperienceGain(grant.Experience, combatWhiteExperienceRecipient(t, spawn.SpawnOperationID) == p.id); packet != nil {
				_ = p.sink.WritePacket(packet)
			}
		}
	}
}

// Java gives the white message to the highest contributor of the highest-damage
// attacker entry (a solo player or party), not necessarily the final hitter.
// Use only the frozen damage/party receipt inputs; ties use the lowest character
// ID, matching the deterministic contributor ordering of our EXP allocation.
func combatWhiteExperienceRecipient(t *combatTicket, spawnID string) int64 {
	party := make(map[int64]int64)
	for _, p := range t.request.Parties {
		for _, id := range p.Members {
			party[id] = p.ID
		}
	}
	type group struct{ party, solo int64 }
	groupFor := func(id int64) group {
		if p := party[id]; p != 0 {
			return group{party: p}
		}
		return group{solo: id}
	}
	for _, target := range t.request.Targets {
		if target.SpawnOperationID != spawnID {
			continue
		}
		totals := make(map[group]int64)
		individual := make(map[int64]int64)
		for _, d := range target.Contributions {
			totals[groupFor(d.CharacterID)] += d.Damage
			individual[d.CharacterID] += d.Damage
		}
		var bestID, bestGroup, bestDamage int64
		for id, damage := range individual {
			groupDamage := totals[groupFor(id)]
			if groupDamage > bestGroup || groupDamage == bestGroup && (damage > bestDamage || damage == bestDamage && (bestID == 0 || id < bestID)) {
				bestID, bestGroup, bestDamage = id, groupDamage, damage
			}
		}
		return bestID
	}
	return 0
}
