package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// dailyResetQuest 7707 (凌晨重置任務) is a v148 client system quest: its
// QuestInfo hides it from every tab and sets autoStart/autoAccept, and its
// Check is only npc 9010000, dayByDay and startscript q7707s. The client asks
// to start it by itself once per day, wherever the character stands, so there
// is no Maple Administrator to talk to. The v145 Quest.wz this server reads
// has no entry for it. The server records it as finished for the day, which
// is all the hidden quest shows, so the client stops asking until tomorrow.
const dailyResetQuest = 7707

var dailyResetZone = time.FixedZone("TMS", 8*3600)

func sameTMSDay(a, b time.Time) bool {
	ay, am, ad := a.In(dailyResetZone).Date()
	by, bm, bd := b.In(dailyResetZone).Date()
	return ay == by && am == bm && ad == bd
}

func (s *Server) completeDailyResetQuest(ctx context.Context, live *liveCharacter, sink packetSink, request protocol.QuestActionRequest, before int) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	snapshot := live.session
	snapshot.Lease = s.currentLease()
	live.mu.Unlock()
	for _, q := range snapshot.Character.Quests {
		if q.ID == dailyResetQuest && q.State == 2 && sameTMSDay(q.CompletedAt, time.Now()) {
			return sink.WritePacket(protocol.EnableActions())
		}
	}
	value, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session:     snapshot,
		OperationID: "daily-reset-quest-" + platform.NewID(),
		Quests:      []domain.ScriptQuestForce{{QuestID: dailyResetQuest, State: 1}, {QuestID: dailyResetQuest, State: 2}},
	})
	if err != nil {
		s.questGMNotice(live, sink, request, before, "失敗("+err.Error()+")")
		return sink.WritePacket(protocol.EnableActions())
	}
	s.applyProgressResult(live, value.Character)
	for _, quest := range value.Quests {
		if err = sink.WritePacket(protocol.QuestUpdate(quest)); err != nil {
			return err
		}
	}
	return sink.WritePacket(protocol.EnableActions())
}
