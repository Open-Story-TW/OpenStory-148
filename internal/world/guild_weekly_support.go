package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const guildWeeklySupportScript = "quest/26000.lua"

// v148 Quest.wz Check 26000 opens q26000s remotely for members who learned
// 91000006. The client offers the conversation only on Monday. The durable
// exchange rechecks the server calendar and the committed guild rank.
func (s *Server) openGuildWeeklySupport(ctx context.Context, channel int, live *liveCharacter, sink packetSink, request protocol.QuestActionRequest) error {
	if request.NPCID != domain.GuildSupportNPCID || (request.Action != 1 && request.Action != 4) {
		return sink.WritePacket(protocol.EnableActions())
	}
	c := s.petSession(live).Character
	if domain.GuildAuditDate(c, time.Now()).In(dailyResetZone).Weekday() != time.Monday {
		return sink.WritePacket(protocol.EnableActions())
	}
	if c.Guild.ID != 0 {
		for _, skill := range c.GuildSkills {
			if skill.SkillID == domain.GuildSupportSkillID && skill.Level > 0 && skill.ExpiresAt == -1 {
				return s.startRemoteScript(ctx, channel, live, sink, domain.GuildSupportNPCID, guildWeeklySupportScript, "公會定期支援", 0)
			}
		}
	}
	return sink.WritePacket(protocol.EnableActions())
}
