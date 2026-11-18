package world

import (
	"os"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// lockContactDamage pins the live victim and a potential Blue Aura caster in
// the same character-ID lock order as a party cast. A stale candidate is
// checked again under the scene and party scopes before any split.
func (s *Server) lockContactDamage(victim *liveCharacter) (*skillMember, func()) {
	victim.mu.Lock()
	c := victim.session.Character
	aura, active := skills.ActiveBlueAura(c, time.Now())
	victim.mu.Unlock()
	var caster *skillMember
	if active && aura.SourceCharacterID != 0 && aura.SourceCharacterID != c.ID {
		s.skills.mu.Lock()
		caster = s.skills.members[aura.SourceCharacterID]
		s.skills.mu.Unlock()
	}
	if caster == nil || caster.live == victim {
		victim.persistMu.Lock()
		return nil, victim.persistMu.Unlock
	}
	if caster.characterID < c.ID {
		caster.live.persistMu.Lock()
		victim.persistMu.Lock()
		return caster, func() {
			victim.persistMu.Unlock()
			caster.live.persistMu.Unlock()
		}
	}
	victim.persistMu.Lock()
	caster.live.persistMu.Lock()
	return caster, func() {
		caster.live.persistMu.Unlock()
		victim.persistMu.Unlock()
	}
}

type blueAuraDamage struct {
	caster domain.Character
	visit  *sceneVisit
}

// applyBlueAuraContact commits only actual accepted hits to the same cast's
// two living holders. It never chooses a substitute caster from the party.
func (s *Server) applyBlueAuraContact(victim, caster *skillMember, raw, hpLoss, mpLoss int, now time.Time) (domain.Character, blueAuraDamage, bool) {
	if caster == nil || s.parties == nil || hpLoss <= 0 || victim.worldID != caster.worldID ||
		victim.channel != caster.channel || victim.mapID != caster.mapID {
		return domain.Character{}, blueAuraDamage{}, false
	}
	source := s.skillSession(caster.live)
	if assetOutcomeUncertain(caster.live, source) != nil || s.combatVitalsPending(caster.live, source) ||
		s.skillMemberFor(source, caster.live, caster.sink) != caster {
		return domain.Character{}, blueAuraDamage{}, false
	}
	a, b := victim.characterID, caster.characterID
	if a > b {
		a, b = b, a
	}
	releaseA, err := s.lockScenePublication(a)
	if err != nil {
		return domain.Character{}, blueAuraDamage{}, false
	}
	defer releaseA()
	releaseB, err := s.lockScenePublication(b)
	if err != nil {
		return domain.Character{}, blueAuraDamage{}, false
	}
	defer releaseB()
	victimVisit := s.sceneFor(victim.live, victim.sink)
	casterVisit := s.sceneFor(caster.live, caster.sink)
	if !s.sceneCurrent(victimVisit) || !s.sceneCurrent(casterVisit) {
		return domain.Character{}, blueAuraDamage{}, false
	}
	s.parties.mu.Lock()
	defer s.parties.mu.Unlock()
	peer, origin := s.parties.peers[victim.characterID], s.parties.peers[caster.characterID]
	if peer == nil || origin == nil || peer.live != victim.live || origin.live != caster.live ||
		peer.party.ID == 0 || peer.party.ID != origin.party.ID || peer.party.Revision != origin.party.Revision {
		return domain.Character{}, blueAuraDamage{}, false
	}
	victim.live.mu.Lock()
	defer victim.live.mu.Unlock()
	caster.live.mu.Lock()
	defer caster.live.mu.Unlock()
	receiver, owner := &victim.live.session.Character, &caster.live.session.Character
	active, receiving := skills.ActiveBlueAura(*receiver, now)
	giving, stillCaster := skills.ActiveBlueAura(*owner, now)
	if !receiving || !stillCaster || active.SourceCharacterID != caster.characterID || giving.SourceCharacterID != caster.characterID ||
		active.ID != giving.ID || active.Value != giving.Value || !active.ExpiresAt.Equal(giving.ExpiresAt) ||
		owner.HP <= 0 || caster.live.god {
		return domain.Character{}, blueAuraDamage{}, false
	}
	victimLoss, casterLoss := skills.BlueAuraContactShare(os.Getenv("MAPLEGO_WZ_ROOT"), active, raw, hpLoss)
	if victimLoss == hpLoss {
		return domain.Character{}, blueAuraDamage{}, false
	}
	receiver.HP = max(0, receiver.HP-victimLoss)
	receiver.MP = max(0, receiver.MP-mpLoss)
	owner.HP = max(0, owner.HP-casterLoss)
	victim.live.dirty = true
	caster.live.dirty = caster.live.dirty || casterLoss > 0
	return *receiver, blueAuraDamage{caster: *owner, visit: casterVisit}, true
}
