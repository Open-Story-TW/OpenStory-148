package world

import (
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// applyItemBuff installs the buff rows a USE item committed into the live
// session and shows them to the owner and visible peers. Caller holds
// persistMu and the scene gate, so live rows still match the committed ones.
func (s *Server) applyItemBuff(live *liveCharacter, sink packetSink, r domain.ItemUseResult) {
	if len(r.Buffs) == 0 {
		return
	}
	live.mu.Lock()
	live.session.Character.SkillBuffs = append([]domain.SkillBuff(nil), r.Character.SkillBuffs...)
	snapshot := cloneCombatSession(live.session)
	live.mu.Unlock()
	s.mu.Lock()
	if prior, ok := s.online[snapshot.CharacterID]; ok && prior.SessionID == snapshot.SessionID && prior.Generation == snapshot.Generation {
		s.online[snapshot.CharacterID] = snapshot
	}
	s.mu.Unlock()
	now := time.Now()
	if s.skills == nil {
		_ = sink.WritePacket(protocol.SkillBuffsAmong(r.Buffs, snapshot.Character.SkillBuffs, now))
		return
	}
	unlock := s.lockSkillPublication()
	defer unlock()
	s.skills.mu.Lock()
	m := s.skills.members[snapshot.CharacterID]
	s.skills.mu.Unlock()
	if m == nil || m.live != live || !sameDropSink(m.sink, sink) {
		_ = sink.WritePacket(protocol.SkillBuffsAmong(r.Buffs, snapshot.Character.SkillBuffs, now))
		return
	}
	peers := s.skillPeers(m.characterID, m.channel, m.mapID)
	s.publishSkillExpiry(m, sink, peers, snapshot.Character, r.Replaced, now)
	_ = sink.WritePacket(protocol.SkillBuffsAmong(r.Buffs, snapshot.Character.SkillBuffs, now))
	for _, target := range peers {
		if s.skillVisible(m, target) {
			_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(m.characterID, r.Buffs))
		}
	}
}
