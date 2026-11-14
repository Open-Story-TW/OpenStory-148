package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The standing GM commands act on other players or the whole world. The store
// has already checked the standing grant and written the audit row; nothing
// here runs under the caller's persistMu.

func gmLines(sink packetSink, lines []string) error {
	for _, line := range lines {
		if err := sink.WritePacket(protocol.PortalTrainingMessage(line)); err != nil {
			return err
		}
	}
	return nil
}

// gmFindOnline looks a character up by name, ignoring case. A character that
// is online but between maps is reported as not ready.
func (s *Server) gmFindOnline(name string) (m *skillMember, session domain.CharacterSession, online bool) {
	s.mu.RLock()
	for _, candidate := range s.online {
		if strings.EqualFold(candidate.Character.Name, name) {
			session, online = candidate, true
			break
		}
	}
	s.mu.RUnlock()
	if !online || s.skills == nil {
		return nil, session, online
	}
	s.skills.mu.Lock()
	m = s.skills.members[session.CharacterID]
	s.skills.mu.Unlock()
	return m, session, true
}

// gmOnlineLines is the !online reply: who is online, and where.
func (s *Server) gmOnlineLines() []string {
	s.mu.RLock()
	players := make([]domain.CharacterSession, 0, len(s.online))
	for _, session := range s.online {
		players = append(players, session)
	}
	s.mu.RUnlock()
	sort.Slice(players, func(i, j int) bool { return players[i].Character.Name < players[j].Character.Name })
	lines := []string{fmt.Sprintf("═════ 線上玩家 %d 人 ═════", len(players))}
	const limit = 30
	for i, p := range players {
		if i == limit {
			lines = append(lines, fmt.Sprintf("……還有 %d 人未列出", len(players)-limit))
			break
		}
		c := p.Character
		lines = append(lines, fmt.Sprintf("　%s　Lv.%d　頻道 %d　地圖 %d", c.Name, c.Level, p.ChannelID, c.MapID))
	}
	return lines
}

// broadcastNotice sends a yellow notice to everyone standing in a map of this
// world; channel 0 is every channel and mapID 0 every map.
func (s *Server) broadcastNotice(channel, mapID int, message string) int {
	if s.maps == nil {
		return 0
	}
	packet := protocol.Notice(message)
	var sinks []packetSink
	s.maps.mu.Lock()
	for key, room := range s.maps.rooms {
		if channel != 0 && key.channel != channel || mapID != 0 && key.mapID != mapID {
			continue
		}
		for _, member := range room {
			sinks = append(sinks, member.sink)
		}
	}
	s.maps.mu.Unlock()
	// Write outside the room lock: a slow connection must not stall movement.
	delivered := 0
	for _, sink := range sinks {
		if sink.WritePacket(packet) == nil {
			delivered++
		}
	}
	return delivered
}

var errGMNoSpawn = errors.New("map has no spawn point")

// gmWarp moves a character to a map's spawn point like an ordinary portal.
func (s *Server) gmWarp(ctx context.Context, channel int, live *liveCharacter, sink packetSink, mapID int) error {
	if s.gameplay == nil {
		return errors.New("gameplay unavailable")
	}
	m, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return err
	}
	entry, ok := m.Spawn()
	if !ok {
		return errGMNoSpawn
	}
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	return s.warpGameplay(ctx, channel, live, sink, m.ID, entry)
}

func (s *Server) gmStanding(session domain.CharacterSession, live *liveCharacter, sink packetSink, req domain.GMCommandRequest) {
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	say := func(format string, a ...any) { _ = gmFeedback(sink, fmt.Sprintf(format, a...)) }
	failed := func(err error) {
		slog.Warn("gm_standing_command_failed", "character", session.CharacterID, "command", req.Name, "error", err)
		say("!%s 沒有完成：%v", req.Name, err)
	}
	// target resolves a name argument to an online character in a map.
	target := func() (*skillMember, domain.CharacterSession, bool) {
		m, found, online := s.gmFindOnline(req.Text)
		switch {
		case !online:
			say("找不到線上玩家「%s」。", req.Text)
		case m == nil:
			say("「%s」正在換地圖，請稍後再試。", found.Character.Name)
		}
		return m, found, m != nil
	}
	switch req.Name {
	case "notice":
		say("公告已送給 %d 位玩家。", s.broadcastNotice(0, 0, req.Text))
	case "map":
		if err := s.gmWarp(ctx, session.ChannelID, live, sink, int(req.Args[0])); err != nil {
			failed(err)
		}
	case "goto":
		m, found, ok := target()
		if !ok {
			return
		}
		if found.ChannelID != session.ChannelID {
			say("「%s」在頻道 %d，請先換到同一個頻道。", found.Character.Name, found.ChannelID)
			return
		}
		if err := s.gmWarp(ctx, session.ChannelID, live, sink, m.mapID); err != nil {
			failed(err)
		}
	case "warphere":
		m, found, ok := target()
		if !ok {
			return
		}
		if found.ChannelID != session.ChannelID {
			say("「%s」在頻道 %d，只能傳送同頻道的玩家。", found.Character.Name, found.ChannelID)
			return
		}
		here := s.skillSession(live).Character.MapID
		if err := s.gmWarp(ctx, m.channel, m.live, m.sink, here); err != nil {
			failed(err)
			return
		}
		say("已把「%s」傳送到這張地圖。", found.Character.Name)
	case "dc":
		m, found, ok := target()
		if !ok {
			return
		}
		c, closable := m.sink.(interface{ Close() error })
		if !closable {
			failed(errors.New("connection cannot be closed"))
			return
		}
		_ = c.Close()
		say("已讓「%s」斷線。", found.Character.Name)
	case "npc":
		if err := s.openPortalNPC(ctx, session.ChannelID, live, sink, int(req.Args[0])); err != nil {
			failed(err)
		}
	case "cash", "points":
		repo, ok := s.repo.(interface {
			AdjustAdminWallet(context.Context, domain.AdminWalletRequest) (domain.CashWallet, error)
		})
		if !ok {
			failed(errors.New("wallet unavailable"))
			return
		}
		currency, label := domain.CashCurrencyCredit, "Gash"
		if req.Name == "points" {
			currency, label = domain.CashCurrencyPoints, "楓葉點數"
		}
		wallet, err := repo.AdjustAdminWallet(ctx, domain.AdminWalletRequest{OperationID: req.OperationID, Actor: fmt.Sprintf("gm-character-%d", session.CharacterID),
			AccountID: session.AccountID, CharacterID: session.CharacterID, Currency: currency, Delta: req.Args[0]})
		if err != nil {
			failed(err)
			return
		}
		say("已增加 %d %s。目前 Gash %d、楓葉點數 %d，進購物商城即可看到。", req.Args[0], label, wallet.Credit, wallet.Points)
	}
}
