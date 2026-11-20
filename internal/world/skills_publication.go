package world

import (
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Lock order: optional caller persistMu -> publication -> short skills.mu,
// live.mu or maps.mu reads, taken separately. A publication never acquires a
// persistMu and never performs SQL. Map Spawn/Remove and all dependent remote
// states share this order; no map/skill registry mutex spans the new writes.
func (s *Server) lockSkillPublication() func() {
	if s.maps != nil {
		s.maps.publication.Lock()
		if s.maps.spawnState == nil {
			s.maps.spawnState = s.publishSpawnSkillStateLocked
		}
		return s.maps.publication.Unlock
	}
	s.skills.publication.Lock()
	return s.skills.publication.Unlock
}

// Requires publication. A skillMember is immutable visit identity; its timing
// counters alone may change under skills.mu.
func (s *Server) skillCurrent(m *skillMember) (domain.CharacterSession, bool) {
	if m == nil || m.live == nil || s.skills == nil {
		return domain.CharacterSession{}, false
	}
	s.skills.mu.Lock()
	registered := s.skills.members[m.characterID] == m
	s.skills.mu.Unlock()
	if !registered {
		return domain.CharacterSession{}, false
	}
	m.live.mu.Lock()
	v := m.live.session
	v.Character.SkillBuffs = append([]domain.SkillBuff(nil), v.Character.SkillBuffs...)
	v.Character.SkillCooldowns = append([]domain.SkillCooldown(nil), v.Character.SkillCooldowns...)
	m.live.mu.Unlock()
	if v.CharacterID != m.characterID || v.SessionID != m.sessionID || v.Generation != m.generation || v.Lease.WorldID != m.worldID || v.ChannelID != m.channel || v.Character.MapID != m.mapID {
		return v, false
	}
	if s.maps != nil {
		s.maps.mu.Lock()
		entry := s.maps.rooms[mapKey{m.channel, m.mapID}][m.characterID]
		valid := entry != nil && entry == m.mapEntry && sameDropSink(entry.sink, m.sink)
		s.maps.mu.Unlock()
		if !valid {
			return v, false
		}
	}
	return v, true
}

func (s *Server) skillMemberFor(session domain.CharacterSession, live *liveCharacter, sink packetSink) *skillMember {
	if s.skills == nil {
		return nil
	}
	s.skills.mu.Lock()
	m := s.skills.members[session.CharacterID]
	s.skills.mu.Unlock()
	if m == nil || m.live != live || !sameDropSink(m.sink, sink) || m.sessionID != session.SessionID || m.generation != session.Generation || m.worldID != session.Lease.WorldID || m.channel != session.ChannelID || m.mapID != session.Character.MapID {
		return nil
	}
	return m
}

func (s *Server) skillPeers(characterID int64, channel, mapID int) []*skillMember {
	if s.skills == nil {
		return nil
	}
	s.skills.mu.Lock()
	defer s.skills.mu.Unlock()
	var out []*skillMember
	for id, peer := range s.skills.members {
		if id != characterID && peer.channel == channel && peer.mapID == mapID {
			out = append(out, peer)
		}
	}
	return out
}

func (s *Server) skillCastAudience(source *skillMember) []*skillMember {
	unlock := s.lockSkillPublication()
	defer unlock()
	if _, valid := s.skillCurrent(source); !valid {
		return nil
	}
	var out []*skillMember
	for _, target := range s.skillPeers(source.characterID, source.channel, source.mapID) {
		if s.skillVisible(source, target) {
			out = append(out, target)
		}
	}
	return out
}

// Requires publication. Visibility is mutual because remote temporary-state
// packets address an already-created CUserRemote; out-of-AOI peers are omitted.
func (s *Server) skillVisible(source, target *skillMember) bool {
	if source == nil || target == nil || source.worldID != target.worldID || source.channel != target.channel || source.mapID != target.mapID {
		return false
	}
	if _, valid := s.skillCurrent(source); !valid {
		return false
	}
	if _, valid := s.skillCurrent(target); !valid {
		return false
	}
	if s.maps == nil {
		return true // Isolated runtime tests have no field registry.
	}
	s.maps.mu.Lock()
	_, ab := source.mapEntry.visible[target.characterID]
	_, ba := target.mapEntry.visible[source.characterID]
	s.maps.mu.Unlock()
	return ab && ba
}

func (s *Server) publishSpawnSkillStateLocked(key mapKey, source, target *mapMember) {
	if s.skills == nil {
		return
	}
	s.skills.mu.Lock()
	a, b := s.skills.members[source.id], s.skills.members[target.id]
	s.skills.mu.Unlock()
	if a == nil || b == nil || a.mapEntry != source || b.mapEntry != target || a.channel != key.channel || a.mapID != key.mapID {
		return
	}
	s.publishCurrentSkillStateLocked(a, b)
}

func (s *Server) publishCurrentSkillStateLocked(source, target *skillMember) {
	if !s.skillVisible(source, target) {
		return
	}
	snapshot, valid := s.skillCurrent(source)
	if !valid || snapshot.Character.HP <= 0 {
		return
	}
	now := time.Now()
	for _, group := range protocol.GroupSkillBuffs(activeSkillBuffs(snapshot.Character, now)) {
		if group[0].ID == sceneLocalSG88 {
			// The remote appearance can be restored to a late observer only
			// while the actor still owns its real robot in this field.
			if s.gameplay == nil {
				continue
			}
			s.gameplay.mu.Lock()
			room := s.gameplay.rooms[mapKey{source.channel, source.mapID}]
			present := room != nil && room.members[source.characterID] != nil &&
				room.members[source.characterID].live == source.live &&
				hasActiveSummon(room, room.members[source.characterID], sceneLocalSG88, now)
			s.gameplay.mu.Unlock()
			if !present {
				continue
			}
		}
		_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(source.characterID, group))
	}
}

func activeSkillBuffs(c domain.Character, now time.Time) []domain.SkillBuff {
	if c.HP <= 0 {
		return nil
	}
	var out []domain.SkillBuff
	for _, buff := range c.SkillBuffs {
		if buff.ExpiresAt.After(now) {
			out = append(out, buff)
		}
	}
	return out
}

// cancelledSkillBuffs keeps the expired rows whose stat no current buff
// re-provides, so a delayed cancellation never clears a replacement.
func cancelledSkillBuffs(c domain.Character, expired []domain.SkillBuff, now time.Time) []domain.SkillBuff {
	var out []domain.SkillBuff
	for _, old := range expired {
		if !activeSkillStat(c, old.Stat, now) {
			out = append(out, old)
		}
	}
	return out
}

// publishSkillExpiry publishes the expired rows of c: a reset for each stat
// no row holds any more and, while buffs stack, the shortened indie list of a
// stat other sources still hold (IndieTempStat::Decode replaces the list, so
// the expired entry leaves the client). Requires publication.
func (s *Server) publishSkillExpiry(source *skillMember, sink packetSink, peers []*skillMember, c domain.Character, expired []domain.SkillBuff, now time.Time) {
	s.publishSkillCancel(source, sink, peers, cancelledSkillBuffs(c, expired, now))
	if !domain.StackBuffsEnabled() {
		return
	}
	var ended []domain.SkillBuff
	done := map[string]bool{}
	for _, old := range expired {
		if !buffstat.Stackable(buffstat.Stat(old.Stat)) || !activeSkillStat(c, old.Stat, now) {
			continue
		}
		ended = append(ended, old)
		if done[old.Stat] {
			continue
		}
		done[old.Stat] = true
		for _, b := range c.SkillBuffs {
			if b.Stat == old.Stat && b.ExpiresAt.After(now) {
				_ = sink.WritePacket(protocol.SkillBuffsFor(c, []domain.SkillBuff{b}, now))
				break
			}
		}
	}
	s.runSkillEnds(source, ended)
}

func (s *Server) publishSkillCancel(source *skillMember, sink packetSink, peers []*skillMember, rows []domain.SkillBuff) {
	for _, group := range protocol.GroupSkillBuffs(rows) {
		_ = sink.WritePacket(protocol.CancelSkillBuffs(0, group))
		for _, target := range peers {
			if s.skillVisible(source, target) {
				_ = target.sink.WritePacket(protocol.CancelSkillBuffs(source.characterID, group))
			}
		}
	}
	s.runSkillEnds(source, rows)
}

// runSkillEnds calls OnEnd for each skill of rows that has no active row
// left on source. Requires publication.
func (s *Server) runSkillEnds(source *skillMember, rows []domain.SkillBuff) {
	if source == nil || len(rows) == 0 {
		return
	}
	var c domain.Character
	loaded := false
	now := time.Now()
	done := map[int]bool{}
	for _, row := range rows {
		h, ok := skills.Lookup(row.ID)
		if done[row.ID] || !ok || h.OnEnd == nil {
			continue
		}
		done[row.ID] = true
		if !loaded {
			current, valid := s.skillCurrent(source)
			if !valid {
				return
			}
			c, loaded = current.Character, true
		}
		if skills.ActiveBuff(c, row.ID, now) {
			continue
		}
		h.OnEnd(&skills.EndContext{SkillID: row.ID, Level: row.Level, Character: c, Now: now})
	}
}

// touchesMaxima reports whether rows change the derived MaxHP/MaxMP, which the
// client only learns from a full vitals stat update.
func touchesMaxima(rows ...[]domain.SkillBuff) bool {
	for _, group := range rows {
		for _, b := range group {
			if b.Stat == "MAXHP" || b.Stat == "MAXMP" {
				return true
			}
			if dice := content.DiceBuffBonuses(b); dice.HPPercent != 0 || dice.MPPercent != 0 {
				return true
			}
		}
	}
	return false
}

func activeSkillStat(c domain.Character, stat string, now time.Time) bool {
	if c.HP <= 0 {
		return false
	}
	for _, buff := range c.SkillBuffs {
		if buffstat.EffectSlot(buff.Stat) == buffstat.EffectSlot(stat) && buff.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (s *Server) publishSkillCast(source *skillMember, castPeers []*skillMember, snapshot domain.CharacterSession, r domain.SkillResult, q protocol.SkillCast) error {
	unlock := s.lockSkillPublication()
	defer unlock()
	current, valid := s.skillCurrent(source)
	if !valid || current.SessionID != snapshot.SessionID || current.Generation != snapshot.Generation || current.ChannelID != snapshot.ChannelID || current.Character.MapID != snapshot.Character.MapID {
		return nil
	}
	if r.Replayed {
		return source.sink.WritePacket(protocol.EnableActions())
	}
	peers := s.skillPeers(snapshot.CharacterID, snapshot.ChannelID, snapshot.Character.MapID)
	now := time.Now()
	s.publishSkillExpiry(source, source.sink, peers, current.Character, r.Expired, now)
	applied := q.SkillID
	if r.Applied != 0 {
		applied = r.Applied
	}
	var cast []domain.SkillBuff
	for _, buff := range current.Character.SkillBuffs {
		if buff.ID != applied || !buff.ExpiresAt.After(now) || current.Character.HP <= 0 {
			continue
		}
		// Reject a delayed result if the same skill has since been recast.
		matched := false
		for _, committed := range snapshot.Character.SkillBuffs {
			matched = matched || committed == buff
		}
		if matched {
			cast = append(cast, buff)
		}
	}
	castActive := len(cast) > 0
	if !castActive && r.Effect && current.Character.HP > 0 {
		// A cost-only, heal or dispel cast has no rows of its own; show it
		// unless the committed result did create rows that are now gone.
		castActive = true
		for _, committed := range snapshot.Character.SkillBuffs {
			castActive = castActive && committed.ID != applied
		}
	}
	if len(r.Inventory) > 0 {
		_ = source.sink.WritePacket(protocol.ModifyInventory(r.Inventory))
	}
	s.publishResetCooldowns(source, r.ResetCooldowns)
	s.publishUpdatedRows(source, peers, current.Character, r.Updated, now)
	if len(cast) > 0 {
		_ = source.sink.WritePacket(protocol.SkillBuffsAmong(cast, current.Character.SkillBuffs, now))
		for _, target := range peers {
			if s.skillVisible(source, target) {
				_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(snapshot.CharacterID, cast))
			}
		}
	}
	for _, cooldown := range current.Character.SkillCooldowns {
		if cooldown.ID == q.SkillID && cooldown.ExpiresAt.After(now) {
			if source.cooldowns == nil {
				source.cooldowns = make(map[int]time.Time)
			}
			source.cooldowns[cooldown.ID] = cooldown.ExpiresAt
			_ = source.sink.WritePacket(protocol.SkillCooldown(q.SkillID, max(1, int(time.Until(cooldown.ExpiresAt).Seconds()+0.999))))
		}
	}
	if castActive {
		effect := func(characterID int64) []byte {
			if r.Dice.First != 0 {
				return protocol.SkillDiceEffect(characterID, q.SkillID, q.Level, r.Dice)
			}
			return protocol.SkillEffect(characterID, q.SkillID, current.Character.Level, q.Level)
		}
		_ = source.sink.WritePacket(effect(0))
		// Only visits present when the cast began receive the transient cast FX.
		// Later entrants receive current state through enter/post-spawn sync.
		for _, target := range castPeers {
			if s.skillVisible(source, target) {
				_ = target.sink.WritePacket(effect(snapshot.CharacterID))
			}
		}
	}
	if touchesMaxima(cast, r.Expired) {
		return source.sink.WritePacket(protocol.InventoryVitals(current.Character))
	}
	return source.sink.WritePacket(protocol.UpdateHPMP(current.Character.HP, current.Character.MP))
}

// publishUpdatedRows re-sends the current rows of every skill whose values
// changed in place (Consume). Requires publication.
func (s *Server) publishUpdatedRows(m *skillMember, peers []*skillMember, c domain.Character, updated []domain.SkillBuff, now time.Time) {
	seen := map[int]bool{}
	for _, u := range updated {
		if seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		var rows []domain.SkillBuff
		for _, b := range activeSkillBuffs(c, now) {
			if b.ID == u.ID {
				rows = append(rows, b)
			}
		}
		if len(rows) == 0 {
			continue
		}
		_ = m.sink.WritePacket(protocol.SkillBuffsFor(c, rows, now))
		for _, target := range peers {
			if s.skillVisible(m, target) {
				_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(m.characterID, rows))
			}
		}
	}
}

// publishResetCooldowns clears cooldowns a cast reset (Time Leap). Requires
// publication.
func (s *Server) publishResetCooldowns(m *skillMember, reset []domain.SkillCooldown) {
	for _, c := range reset {
		delete(m.cooldowns, c.ID)
		_ = m.sink.WritePacket(protocol.SkillCooldown(c.ID, 0))
	}
}

func (s *Server) publishSkillForeign(snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, packet []byte) {
	if s.skills == nil {
		return
	}
	unlock := s.lockSkillPublication()
	defer unlock()
	source := s.skillMemberFor(snapshot, live, sink)
	if source == nil {
		return
	}
	for _, target := range s.skillPeers(snapshot.CharacterID, snapshot.ChannelID, snapshot.Character.MapID) {
		if s.skillVisible(source, target) {
			_ = target.sink.WritePacket(packet)
		}
	}
}

// snapshot identifies a committed event, not a replaceable current room.
// Caller owns source persistMu; old delayed events are checked against current
// buff state so an old cancellation cannot clear a replacement of the same stat.
func (s *Server) publishSkillTick(snap domain.CharacterSession, sink packetSink, r domain.SkillResult, previousHP, previousMP int) {
	if s.skills == nil {
		return
	}
	unlock := s.lockSkillPublication()
	defer unlock()
	s.skills.mu.Lock()
	m := s.skills.members[snap.CharacterID]
	s.skills.mu.Unlock()
	if m == nil || !sameDropSink(m.sink, sink) || m.sessionID != snap.SessionID || m.generation != snap.Generation || m.channel != snap.ChannelID || m.mapID != snap.Character.MapID || m.worldID != snap.Lease.WorldID {
		return
	}
	current, valid := s.skillCurrent(m)
	if !valid {
		return
	}
	peers := s.skillPeers(snap.CharacterID, snap.ChannelID, snap.Character.MapID)
	s.publishSkillExpiry(m, sink, peers, current.Character, r.Expired, time.Now())
	s.publishUpdatedRows(m, peers, current.Character, r.Updated, time.Now())
	if touchesMaxima(r.Expired) {
		_ = sink.WritePacket(protocol.InventoryVitals(current.Character))
	} else if current.Character.HP != previousHP || current.Character.MP != previousMP || len(r.Updated) > 0 {
		_ = sink.WritePacket(protocol.UpdateHPMP(current.Character.HP, current.Character.MP))
	}
}
