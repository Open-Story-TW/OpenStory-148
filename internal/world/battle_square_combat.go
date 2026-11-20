package world

import (
	"log/slog"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

type battleSquareGuard struct {
	skillID int
	rating  int
}

// battleSquareGuardAttack mirrors v148 CSkillInfo's intrinsic level 1 for the
// job's arena passive (0x6D4735) and the 9999 clamp/divide/multiply at 0x9ACF9A.
// Only current, active arena members reach this calculation.
func battleSquareGuardAttack(c domain.Character, defense int) battleSquareGuard {
	// CSkillInfo's v148 job selector (0x6ABE27) chooses these four
	// defense-conversion skills for the corresponding warrior families.
	for _, id := range [...]int{1000007, 11000006, 21000005, 31000005} {
		if !domain.CanLearnSkill(c.Job, id) {
			continue
		}
		d, err := content.ConfiguredSkillDefinition(id)
		if err != nil || d.Disabled {
			return battleSquareGuard{}
		}
		v, err := d.Values(1)
		if err != nil || v["x"] <= 0 {
			return battleSquareGuard{}
		}
		return battleSquareGuard{skillID: id, rating: min(9999, max(0, defense)) / v["x"] * v["PVPdamage"]}
	}
	return battleSquareGuard{}
}

// battleSquareBasicDamage applies the existing Legacy survival damage basis
// to the ordinary melee swing. Ranking brackets below 3 use base attributes.
// All target defenses and player positions are authoritative server values.
func battleSquareBasicDamage(actor domain.Character, atk, def combat.PlayerStats, rank uint8, guard int) int {
	weapon := combat.WeaponFor(atk.WeaponID)
	str, dex, intl, luk := actor.STR, actor.DEX, actor.INT, actor.LUK
	if rank == 3 {
		str, dex, intl, luk = atk.STR, atk.DEX, atk.INT, atk.LUK
	}
	main, sub := str, dex
	switch {
	case weapon.Luck:
		main, sub = luk, dex+str
	case weapon.DexMain:
		main, sub = dex, str
	case combat.IsMageJob(atk.Job):
		main, sub = intl, luk
	}
	// Keep the fractional PvP rating until the final floor.
	base := int64(weapon.Multiplier) * int64(4*main+sub) * int64(10000+guard)
	denom := int64(100*100) * int64(max(2, def.WDEF)) / 2
	return int(min(99999, max(1, base/denom)))
}

func (s *Server) handleBattleSquareAttack(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	q, err := protocol.ReadBattleSquareBasicAttack(r)
	if err != nil || s.events == nil || s.gameplay == nil {
		return nil
	}
	source := partySession(live)
	if source.Character.HP <= 0 || !s.sceneCurrent(s.sceneFor(live, sink)) {
		return nil
	}
	inst := s.eventOf(source.CharacterID)
	if inst == nil || inst.battleSquare == nil || !inst.definition.Stage(source.Character.MapID) {
		return nil
	}
	now := time.Now()
	s.events.mu.Lock()
	match := inst.battleSquare
	canAttack := !inst.ended && match.started && match.arrived[source.CharacterID] && now.Sub(match.lastAttack[source.CharacterID]) >= attackInterval(0)
	if canAttack {
		match.lastAttack[source.CharacterID] = now
	}
	rank := match.rank
	s.events.mu.Unlock()
	if !canAttack {
		return nil
	}
	stats, err := skills.BattleSquareMeleeStats(source.Character, now)
	if err != nil || combat.WeaponFor(stats.WeaponID).Ranged {
		return nil
	}
	guard := battleSquareGuardAttack(source.Character, stats.WDEF)
	for _, o := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		if o.live == live {
			continue
		}
		victim := partySession(o.live)
		if victim.Character.HP <= 0 || !battleSquareWithin(source.Character, victim.Character, q.FacingLeft) || s.eventOf(victim.CharacterID) != inst {
			continue
		}
		if applied := s.battleSquareBasicHit(inst, live, sink, source, o, victim, stats, rank, guard, q.FacingLeft, now); applied {
			break
		}
	}
	return nil
}

func battleSquareWithin(source, target domain.Character, left bool) bool {
	dx := target.X - source.X
	if left {
		dx = -dx
	}
	return dx >= 0 && dx <= 105 && abs(target.Y-source.Y) <= 45
}

func (s *Server) battleSquareBasicHit(inst *eventInstance, live *liveCharacter, sink packetSink, source domain.CharacterSession, target eventOccupant, receiver domain.CharacterSession, stats combat.PlayerStats, rank uint8, guard battleSquareGuard, left bool, now time.Time) bool {
	first, second := live, target.live
	if source.CharacterID > receiver.CharacterID {
		first, second = second, first
	}
	first.persistMu.Lock()
	defer first.persistMu.Unlock()
	second.persistMu.Lock()
	defer second.persistMu.Unlock()
	sourceNow, current := partySession(live), partySession(target.live)
	if !sameSceneOwner(receiver, current) || !sameSceneOwner(source, sourceNow) || sourceNow.Character.HP <= 0 ||
		current.Character.HP <= 0 || !s.sceneCurrent(s.sceneFor(target.live, target.sink)) || !s.sceneCurrent(s.sceneFor(live, sink)) ||
		s.combatVitalsPending(target.live, current) || s.combatVitalsPending(live, sourceNow) ||
		!battleSquareWithin(sourceNow.Character, current.Character, left) {
		return false
	}
	defender, err := skills.BattleSquareMeleeStats(current.Character, now)
	if err != nil {
		return false
	}
	s.events.mu.Lock()
	if inst.ended || !inst.battleSquare.started || s.events.byMember[source.CharacterID] != inst || s.events.byMember[current.CharacterID] != inst || !inst.battleSquare.arrived[current.CharacterID] {
		s.events.mu.Unlock()
		return false
	}
	// Matched players may only affect current scene participants. Keep the
	// match lock through the single HP publication decision, not I/O.
	target.live.mu.Lock()
	damage := battleSquareBasicDamage(source.Character, stats, defender, rank, guard.rating)
	before := target.live.session.Character.HP
	// Survival respawn/result packets are a separate path. Preserve a live
	// opponent in this basic-hit lane until they can opt out of the match.
	after := max(1, before-damage)
	target.live.session.Character.HP = after
	target.live.dirty = true
	target.live.mu.Unlock()
	s.events.mu.Unlock()
	_ = target.sink.WritePacket(protocol.UpdateHP(after))
	slog.Info("battle_square_basic_hit", "attacker", source.CharacterID, "victim", current.CharacterID,
		"guard_skill", guard.skillID, "guard", guard.rating, "wdef", stats.WDEF, "hp_before", before, "hp_after", after, "match", inst.id)
	return true
}
