package world

import (
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Channel/cash handoff can replace the world member while the native context
// survives. Explicitly synchronize the two currently implemented cooldown
// skills, including zero for an expiry filtered out by the database loader.
// Caller holds publication; no content I/O or repository access occurs here.
func (s *Server) syncNativeSkillCooldownsLocked(member *skillMember, c domain.Character, now time.Time) error {
	for _, skill := range c.Skills {
		if skill.Level <= 0 || (skill.ID != 1001 && skill.ID != 1002) {
			continue
		}
		var expiry time.Time
		for _, cooldown := range c.SkillCooldowns {
			if cooldown.ID == skill.ID && cooldown.ExpiresAt.After(expiry) {
				expiry = cooldown.ExpiresAt
			}
		}
		seconds := 0
		if expiry.After(now) {
			seconds = max(1, int(expiry.Sub(now).Seconds()+0.999))
			member.cooldowns[skill.ID] = expiry
		}
		if err := member.sink.WritePacket(protocol.SkillCooldown(skill.ID, seconds)); err != nil {
			return err
		}
		if seconds == 0 {
			delete(member.cooldowns, skill.ID)
		}
	}
	return nil
}

// The native client tests presence in its cooldown map, not whether the stored
// deadline has passed (7D92D8, called at8A6B6B). OnSkillCooltimeSet869E98 removes
// the entry only for seconds=0. Track announced deadlines independently of the
// database loader, which already excludes expired rows, and of Buff lifetime.
// Like other skill publication this never acquires persistMu or performs SQL.
func (s *Server) expireSkillCooldowns(member *skillMember, now time.Time) {
	unlock := s.lockSkillPublication()
	defer unlock()
	current, valid := s.skillCurrent(member)
	if !valid {
		return
	}
	for id, deadline := range member.cooldowns {
		if deadline.After(now) {
			continue
		}
		// A recast or result refresh may already own a newer cooldown. An old
		// timer must not remove that current native entry.
		active := false
		for _, cooldown := range current.Character.SkillCooldowns {
			if cooldown.ID == id && cooldown.ExpiresAt.After(now) {
				member.cooldowns[id] = cooldown.ExpiresAt
				active = true
				break
			}
		}
		if !active && member.sink.WritePacket(protocol.SkillCooldown(id, 0)) == nil {
			delete(member.cooldowns, id)
		}
	}
}
