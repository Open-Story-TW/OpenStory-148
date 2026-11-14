package world

import (
	"bytes"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// publishStatDetail sends the launcher's stat detail (0x614) when it differs
// from the last one this connection was sent. Both field entries (initial
// scene and warp) force it; the skill ticker then re-derives it every second,
// which catches every source at once (equipment, buffs gained or run out,
// passives, job, runtime rates) without a call at each place that changes
// one. Like 0x610 it is sent to every client: all of them run the launcher.
func (s *Server) publishStatDetail(live *liveCharacter, sink packetSink, now time.Time, force bool) {
	if s.skills == nil {
		return // No skill space, no ticker to keep it current.
	}
	live.mu.Lock()
	c := live.session.Character
	c.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	c.Skills = append([]domain.SkillState(nil), c.Skills...)
	c.SkillBuffs = append([]domain.SkillBuff(nil), c.SkillBuffs...)
	live.mu.Unlock()
	packet := protocol.StatDetailPacket(skills.StatDetail(c, now))
	live.mu.Lock()
	same := bytes.Equal(live.statDetail, packet)
	live.statDetail = packet
	live.mu.Unlock()
	if same && !force {
		return
	}
	if sink.WritePacket(packet) != nil {
		// Unknown delivery: let the next pass send it again.
		live.mu.Lock()
		live.statDetail = nil
		live.mu.Unlock()
	}
}
