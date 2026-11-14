package world

import (
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Caller holds gameplay.mu. Native8834D8/42D8F4 selects this owner's
// Poison Mist objects, not every mist in the map. Never substitute a new
// object or another field visit for a client-named object.
func ownedEruptionAreasLocked(room *gameplayRoom, member *gameplayMember, session domain.CharacterSession, attack protocol.Attack, effect combat.Effect, now time.Time) ([]*roomArea, error) {
	if attack.SkillID != 2121003 {
		if len(attack.EruptionAreas) != 0 {
			return nil, domain.ErrInvalidInput
		}
		return nil, nil
	}
	if attack.Opcode != protocol.RecvMagicAttack || len(attack.EruptionAreas) < 1 || len(attack.EruptionAreas) > min(effect.Y, 6) {
		return nil, domain.ErrInvalidInput
	}
	if !effect.Box.Set {
		return nil, domain.ErrInvalidInput
	}
	lt, rb := effect.Box.At(combat.Point{X: session.Character.X, Y: session.Character.Y}, attack.FacingLeft())
	seen := map[int32]bool{}
	areas := make([]*roomArea, 0, len(attack.EruptionAreas))
	for _, id := range attack.EruptionAreas {
		a := room.areas[id]
		if id <= 0 || seen[id] || a == nil || a.skillID != 2111003 || a.ownerID != session.CharacterID || a.ownerSession != session.SessionID || a.ownerGeneration != session.Generation || a.ownerEntry != member || a.reservation != nil || !a.expiresAt.After(now) {
			return nil, domain.ErrForbidden
		}
		// Native42D8F4 uses Win32 PtInRect on the mist's creation point:
		// left/top inclusive, right/bottom exclusive. Never use client XY.
		if a.x < lt.X || a.x >= rb.X || a.y < lt.Y || a.y >= rb.Y {
			return nil, domain.ErrForbidden
		}
		seen[id] = true
		areas = append(areas, a)
	}
	return areas, nil
}

func (s *Server) eruptionClaimsCurrentLocked(t *combatTicket, now time.Time) bool {
	if t.generic == nil || len(t.generic.eruptAreas) == 0 {
		return true
	}
	room := s.gameplay.rooms[t.key]
	if room == nil || room.members[t.actorSession.CharacterID] != t.expectedActorEntry {
		return false
	}
	for _, a := range t.generic.eruptAreas {
		if room.areas[a.objectID] != a || a.reservation != t || a.ownerEntry != t.expectedActorEntry || !a.expiresAt.After(now) {
			return false
		}
	}
	return true
}

// Known rollback releases ownership; unknown outcomes retain it. The normal
// area ticker then handles an expired object without inventing a new cast.
func releaseEruptionClaimsLocked(t *combatTicket) {
	if t.generic == nil {
		return
	}
	for _, a := range t.generic.eruptAreas {
		if a.reservation == t {
			a.reservation = nil
		}
	}
}
