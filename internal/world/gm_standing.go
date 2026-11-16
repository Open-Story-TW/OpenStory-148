package world

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/settings"
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

// gmKillAll removes the field's ordinary monsters without EXP or drops. They
// stay in the room as dead, so the usual respawn brings them back. A monster
// in a pending settlement is left to it, and a spawned (QA) one to !clearmobs.
func (s *Server) gmKillAll(key mapKey) int {
	g := s.gameplay
	if g == nil {
		return 0
	}
	now := time.Now()
	var deliveries []roomDelivery
	killed := 0
	g.mu.Lock()
	if room := g.rooms[key]; room != nil {
		for _, mob := range room.mobs {
			if mob.qaOwner != nil || mob.hp <= 0 || mob.claiming || mob.reservation != nil {
				continue
			}
			mob.hp = 0
			mob.respawnAt = mob.respawnTime(key.mapID, now)
			mob.statuses, mob.contributions = nil, nil
			deliveries = append(deliveries, roomBroadcast(room, protocol.KillMonster(mob.objectID), -1)...)
			killed++
		}
	}
	g.mu.Unlock()
	deliver(deliveries)
	return killed
}

func gmActor(session domain.CharacterSession) string {
	return fmt.Sprintf("gm-character-%d", session.CharacterID)
}

// gmKick closes the connections of the given characters that are online here.
func (s *Server) gmKick(ids []int64) int {
	if s.skills == nil {
		return 0
	}
	var sinks []packetSink
	s.skills.mu.Lock()
	for _, id := range ids {
		if m := s.skills.members[id]; m != nil {
			sinks = append(sinks, m.sink)
		}
	}
	s.skills.mu.Unlock()
	kicked := 0
	for _, sink := range sinks {
		if c, ok := sink.(interface{ Close() error }); ok {
			_ = c.Close()
			kicked++
		}
	}
	return kicked
}

type gmBanStore interface {
	BanAccount(ctx context.Context, character string, until time.Time, reason, actor string) (domain.GMBan, error)
	UnbanAccount(ctx context.Context, character string) (domain.GMBan, error)
	LastLoginIP(ctx context.Context, character string) (string, error)
	BanIP(ctx context.Context, ip, reason, actor string) error
	UnbanIP(ctx context.Context, ip string) error
}

func (s *Server) gmBan(ctx context.Context, session domain.CharacterSession, req domain.GMCommandRequest, say func(string, ...any), failed func(error)) {
	store, ok := s.repo.(gmBanStore)
	if !ok {
		failed(errors.New("bans unavailable"))
		return
	}
	actor := gmActor(session)
	switch req.Name {
	case "ban":
		if strings.EqualFold(req.Text, session.Character.Name) {
			say("不能封鎖自己。")
			return
		}
		until, length := domain.QAGMPermanentExpiry, "永久"
		if len(req.Args) == 1 {
			until, length = time.Now().Add(time.Duration(req.Args[0])*24*time.Hour), fmt.Sprintf("%d 天", req.Args[0])
		}
		ban, err := store.BanAccount(ctx, req.Text, until, "GM "+session.Character.Name, actor)
		if errors.Is(err, domain.ErrNotFound) {
			say("找不到角色「%s」。", req.Text)
			return
		}
		if err != nil {
			failed(err)
			return
		}
		say("已封鎖「%s」的帳號 %s（%s），並讓 %d 個線上角色斷線。", req.Text, ban.Account, length, s.gmKick(ban.CharacterIDs))
	case "unban":
		ban, err := store.UnbanAccount(ctx, req.Text)
		if errors.Is(err, domain.ErrNotFound) {
			say("找不到角色「%s」。", req.Text)
			return
		}
		if err != nil {
			failed(err)
			return
		}
		say("已解除「%s」的帳號 %s 的封鎖。", req.Text, ban.Account)
	case "banip":
		// An online player's live address; otherwise the last one they
		// entered the world from.
		ip := ""
		var kick []int64
		if m, found, online := s.gmFindOnline(req.Text); online && m != nil {
			if remote, ok := m.sink.(interface{ RemoteAddr() net.Addr }); ok {
				ip, _, _ = net.SplitHostPort(remote.RemoteAddr().String())
			}
			kick = []int64{found.CharacterID}
		}
		if ip == "" {
			last, err := store.LastLoginIP(ctx, req.Text)
			if errors.Is(err, domain.ErrNotFound) {
				say("找不到「%s」的登入紀錄。", req.Text)
				return
			}
			if err != nil {
				failed(err)
				return
			}
			ip = last
		}
		if err := store.BanIP(ctx, ip, "GM "+session.Character.Name+" / "+req.Text, actor); err != nil {
			if errors.Is(err, domain.ErrInvalidInput) {
				say("「%s」的位址 %s 不能封鎖（本機位址）。", req.Text, ip)
				return
			}
			failed(err)
			return
		}
		say("已封鎖「%s」的 IP %s，並讓 %d 個線上角色斷線。解除請用 !unbanip %s", req.Text, ip, s.gmKick(kick), ip)
	case "unbanip":
		err := store.UnbanIP(ctx, req.Text)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			say("IP %s 沒有被封鎖。", req.Text)
		case errors.Is(err, domain.ErrInvalidInput):
			say("「%s」不是有效的 IP 位址。", req.Text)
		case err != nil:
			failed(err)
		default:
			say("已解除 IP %s 的封鎖。", req.Text)
		}
	}
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
	case "hide":
		// The store changed the buff row and the caller saw it; now the map.
		live.persistMu.Lock()
		s.syncGMHide(live)
		live.persistMu.Unlock()
		if req.Args[0] == 1 {
			say("已隱身：其他玩家看不到你。輸入 !hide 0 解除。")
		} else {
			say("已解除隱身。")
		}
	case "god":
		live.mu.Lock()
		live.god = req.Args[0] == 1
		live.mu.Unlock()
		if req.Args[0] == 1 {
			say("已開啟無敵：怪物的傷害不會扣血。換頻道或重新登入後要重新開啟。")
		} else {
			say("已關閉無敵。")
		}
	case "killall":
		say("已清除這張地圖的 %d 隻怪物，牠們會照平常的時間重生。", s.gmKillAll(mapKey{channel: session.ChannelID, mapID: s.skillSession(live).Character.MapID}))
	case "cleardrops":
		store, ok := s.repo.(interface {
			ClearMapDrops(context.Context, domain.WorldLease, int, int) ([]domain.ItemDrop, error)
		})
		if !ok {
			failed(errors.New("drops unavailable"))
			return
		}
		drops, err := store.ClearMapDrops(ctx, s.currentLease(), session.ChannelID, s.skillSession(live).Character.MapID)
		if err != nil {
			failed(err)
			return
		}
		for _, drop := range drops {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		say("已清除這張地圖的 %d 個掉落物。", len(drops))
	case "ban", "unban", "banip", "unbanip":
		s.gmBan(ctx, session, req, say, failed)
	case "rate":
		store, ok := s.repo.(interface{ Database() *sql.DB })
		if !ok {
			failed(errors.New("settings unavailable"))
			return
		}
		key, label := "exp_rate", "經驗"
		switch req.Args[0] {
		case 2:
			key, label = "meso_rate", "楓幣"
		case 3:
			key, label = "drop_rate", "掉寶"
		}
		if err := settings.Save(ctx, store.Database(), gmActor(session), map[string]string{key: strconv.FormatInt(req.Args[1], 10)}); err != nil {
			failed(err)
			return
		}
		// Every service reads the saved value within a few seconds; this
		// world applies it at once.
		exp, meso, drop := int(domain.ExpRate()), int(domain.MesoRate()), int(domain.DropRate())
		switch req.Args[0] {
		case 1:
			exp = int(req.Args[1])
		case 2:
			meso = int(req.Args[1])
		case 3:
			drop = int(req.Args[1])
		}
		domain.SetRates(exp, meso, drop)
		say("%s倍率已改為 x%d。目前：經驗 x%d、楓幣 x%d、掉寶 x%d。", label, req.Args[1], exp, meso, drop)
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
