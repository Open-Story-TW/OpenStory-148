package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Commands are self-only and delivered by ordinary native chat. Parsing never
// accepts a target identity, arbitrary SQL, script source, or a packet payload.
func parseGMChat(text string) (string, []int64, bool) {
	if !strings.HasPrefix(text, "!") {
		return "", nil, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", nil, true
	}
	name := strings.TrimPrefix(fields[0], "!")
	want := -1
	switch name {
	case "heal", "whereami", "effstats", "clearmobs", "online", "rates", "maxskills", "killall", "cleardrops": // !effstats: read-only server stat view
		want = 0
	case "help": // !help [page]
		want = len(fields) - 1
		if want > 1 {
			want = -1
		}
	case "level", "job", "warp", "buffreset", "hp", "ap", "sp", "map", "npc", "cash", "points", "hair", "face", "skin", "fame", "hide", "god":
		want = 1
	case "petfood", "mesos", "alliance", "mobhold": // !mobhold <0|1>
		want = 1
	case "item", "debuff": // !item <id> [quantity], !debuff <bit> [seconds]
		want = 1
		if len(fields) == 3 {
			want = 2
		}
	case "skill", "cashexpire", "petexpire", "vitals": // !vitals <maxhp> <maxmp>
		want = 2
	case "spawn": // !spawn <mob> <count> [centerX]
		want = 2
		if len(fields) == 4 {
			want = 3
		}
	case "stats": // !stats <str> <dex> <int> <luk>
		want = 4
	}
	if want < 0 || len(fields) != want+1 {
		return "", nil, true
	}
	args := make([]int64, want)
	for i, v := range fields[1:] {
		// Only spawn's optional X accepts a leading minus. Other arguments
		// retain the existing unsigned decimal grammar.
		digits := v
		if name == "spawn" && i == 2 && strings.HasPrefix(v, "-") {
			digits = v[1:]
		}
		if digits == "" {
			return "", nil, true
		}
		for _, c := range digits {
			if c < '0' || c > '9' {
				return "", nil, true
			}
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "", nil, true
		}
		args[i] = n
	}
	return name, args, true
}

// gmHideBit is GM_HIDE in the buff stat table.
const gmHideBit = 56

// parseGMCommand adds the commands with one text argument (a character name
// or a notice) to parseGMChat's numeric grammar.
func parseGMCommand(text string) (name string, args []int64, arg string, handled bool) {
	fields := strings.Fields(text)
	if len(fields) > 0 {
		switch fields[0] {
		case "!fitness", "!ola", "!treasure", "!snowball", "!coconut":
			if len(fields) == 2 {
				for index, action := range [...]string{"open", "start", "cancel"} {
					if fields[1] == action {
						return fields[0][1:], []int64{int64(index + 1)}, "", true
					}
				}
			}
			return "", nil, "", true
		case "!oxquiz":
			if len(fields) != 2 {
				return "", nil, "", true
			}
			switch fields[1] {
			case "open":
				return "oxquiz", []int64{1}, "", true
			case "start":
				return "oxquiz", []int64{2}, "", true
			case "cancel":
				return "oxquiz", []int64{3}, "", true
			}
			return "", nil, "", true
		case "!notice":
			arg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "!notice"))
			if arg == "" || len(arg) > domain.GMNoticeMax {
				return "", nil, "", true
			}
			return "notice", nil, arg, true
		case "!goto", "!warphere", "!dc", "!unban", "!banip", "!unbanip":
			if len(fields) != 2 || len(fields[1]) > domain.GMNameMax {
				return "", nil, "", true
			}
			return fields[0][1:], nil, fields[1], true
		case "!ban": // !ban <name> [days]
			if len(fields) < 2 || len(fields) > 3 || len(fields[1]) > domain.GMNameMax {
				return "", nil, "", true
			}
			if len(fields) == 3 {
				days, err := strconv.ParseInt(fields[2], 10, 64)
				if err != nil || days < 1 {
					return "", nil, "", true
				}
				args = []int64{days}
			}
			return "ban", args, fields[1], true
		case "!rate": // !rate <exp|meso|drop> <multiplier>
			kind := map[string]int64{"exp": 1, "經驗": 1, "meso": 2, "楓幣": 2, "drop": 3, "掉寶": 3}
			if len(fields) != 3 || kind[strings.ToLower(fields[1])] == 0 {
				return "", nil, "", true
			}
			n, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || n < 1 {
				return "", nil, "", true
			}
			return "rate", []int64{kind[strings.ToLower(fields[1])], n}, "", true
		}
	}
	name, args, handled = parseGMChat(text)
	return name, args, "", handled
}

// The !help reply: a menu of pages, then one page per !help <number>, one
// command a line. Every command the parser accepts is listed (a test checks).
type gmHelpEntry struct{ usage, about string }
type gmHelpPage struct {
	title   string
	entries []gmHelpEntry
}

var gmHelpPages = []gmHelpPage{
	{"角色", []gmHelpEntry{
		{"!heal", "補滿 HP 和 MP"},
		{"!hp <數值>", "設定目前 HP，0 會死亡"},
		{"!level <等級>", "升到指定等級，只能往上"},
		{"!job <職業代碼>", "轉職"},
		{"!stats <力量> <敏捷> <智力> <幸運>", "設定四項能力值"},
		{"!vitals <最大HP> <最大MP>", "設定最大 HP 和 MP 並補滿"},
		{"!ap <點數>", "設定未分配的能力點"},
		{"!sp <點數>", "設定未分配的技能點"},
		{"!fame <名聲>", "設定名聲"},
		{"!hair <髮型代碼>", "換髮型"},
		{"!face <臉型代碼>", "換臉型"},
		{"!skin <膚色編號>", "換膚色，0 到 5 或 9 到 11"},
		{"!hide <0或1>", "1 隱身，其他玩家看不到你"},
		{"!god <0或1>", "1 無敵，怪物打不扣血"},
	}},
	{"技能", []gmHelpEntry{
		{"!skill <技能代碼> <等級>", "學會技能，不扣技能點"},
		{"!maxskills", "目前職業能學的技能全部點滿"},
		{"!debuff <狀態編號> [秒數]", "讓自己中異常狀態"},
		{"!buffreset <狀態編號>", "清除一種狀態"},
	}},
	{"物品", []gmHelpEntry{
		{"!item <道具代碼> [數量]", "取得道具"},
		{"!mesos <金額>", "取得楓幣"},
		{"!cash <數量>", "取得 Gash"},
		{"!points <數量>", "取得楓葉點數"},
		{"!petfood <數量>", "取得寵物飼料"},
		{"!petexpire <寵物序號> <秒>", "設定寵物幾秒後到期"},
		{"!cashexpire <保管箱序號> <秒>", "設定商城道具幾秒後到期"},
	}},
	{"地圖", []gmHelpEntry{
		{"!map <地圖代碼>", "傳送到任何地圖"},
		{"!warp <地圖代碼>", "傳送到測試地圖"},
		{"!whereami", "顯示所在地圖和座標"},
		{"!npc <NPC代碼>", "不用走到 NPC 旁就開啟對話"},
		{"!spawn <怪物代碼> <數量> [X座標]", "召喚怪物；永久授權可在任何地圖"},
		{"!killall", "清除這張地圖的怪物，不給經驗和掉寶"},
		{"!cleardrops", "清除這張地圖的掉落物"},
		{"!clearmobs", "清除自己召喚的怪物"},
		{"!mobhold <0或1>", "1 讓測試地圖的怪物打不死"},
	}},
	{"玩家", []gmHelpEntry{
		{"!online", "列出線上玩家和所在地圖"},
		{"!goto <角色名稱>", "傳送到該玩家所在地圖"},
		{"!warphere <角色名稱>", "把該玩家傳送到自己的地圖"},
		{"!dc <角色名稱>", "讓該玩家斷線"},
		{"!notice <內容>", "對全伺服器發公告"},
		{"!ban <角色名稱> [天數]", "封鎖該角色的帳號，不寫天數為永久"},
		{"!unban <角色名稱>", "解除帳號封鎖"},
		{"!banip <角色名稱>", "封鎖該玩家的 IP"},
		{"!unbanip <IP>", "解除 IP 封鎖"},
		{"!alliance <對方角色ID>", "建立聯盟，0 為解散"},
	}},
	{"伺服器", []gmHelpEntry{
		{"!rates", "顯示經驗、楓幣、掉寶倍率"},
		{"!rate <exp或meso或drop> <倍數>", "修改倍率，立即生效"},
		{"!oxquiz <open|start|cancel>", "開放報名、開始或取消本頻道的 OX 問答"},
		{"!treasure <open|start|cancel>", "開放報名、開始或取消本頻道的黃金傳說尋寶"},
		{"!snowball <open|start|cancel>", "開放報名、開始或取消本頻道的滾雪球"},
		{"!coconut <open|start|cancel>", "開放報名、開始或取消本頻道的打椰子"},
		{"!fitness <open|start|cancel>", "開放報名、開始或取消本頻道的體能測驗"},
		{"!ola <open|start|cancel>", "開放報名、開始或取消本頻道的向上攀升"},
		{"!effstats", "顯示伺服器計算的攻擊數值"},
		{"!help [編號]", "顯示這份說明"},
	}},
}

// gmHelpLines returns the menu (page 0) or one page; ok is false for a page
// that does not exist. ★ marks a command that needs a standing grant.
func gmHelpLines(page int) (lines []string, ok bool) {
	if page == 0 {
		lines = []string{"═════ GM 指令說明 ═════", "輸入 !help 加編號，查看該類指令："}
		row := ""
		for i, p := range gmHelpPages {
			row += fmt.Sprintf("　%d %s（%d 個）", i+1, p.title, len(p.entries))
			if i%3 == 2 || i == len(gmHelpPages)-1 {
				lines = append(lines, row)
				row = ""
			}
		}
		return append(lines, "例：!help 1　　< > 必填，[ ] 可省略"), true
	}
	if page < 1 || page > len(gmHelpPages) {
		return nil, false
	}
	p := gmHelpPages[page-1]
	lines = []string{fmt.Sprintf("═════ %d %s ═════", page, p.title)}
	standing := false
	for _, e := range p.entries {
		mark := "　"
		if domain.GMStandingCommand(strings.TrimPrefix(strings.Fields(e.usage)[0], "!")) {
			mark, standing = "★", true
		}
		lines = append(lines, mark+e.usage+"　→　"+e.about)
	}
	if standing {
		lines = append(lines, "★ 需要後台給的永久 GM 授權")
	}
	return lines, true
}

func (s *Server) gmBusy(live *liveCharacter) bool {
	if s.tradeActive(live) || s.tradeDialogBusy(live) {
		return true
	}
	live.mu.Lock()
	queue := live.mapEntry
	live.mu.Unlock()
	if queue != nil {
		queue.mu.Lock()
		busy := queue.pending != nil || queue.running || queue.scene != nil
		queue.mu.Unlock()
		if busy {
			return true
		}
	}
	if s.partySkillVitalsPending(live, s.skillSession(live)) {
		return true
	}
	if s.gameplay != nil {
		s.gameplay.mu.Lock()
		defer s.gameplay.mu.Unlock()
		for _, ticket := range s.gameplay.pendingCombat {
			if ticket.actor == live {
				return true
			}
			for _, peer := range ticket.peers {
				if peer.live == live {
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) gmCurrent(session domain.CharacterSession, live *liveCharacter, sink packetSink) bool {
	s.mu.RLock()
	owner, ok := s.online[session.CharacterID]
	s.mu.RUnlock()
	if !ok || !sameSceneOwner(owner, session) || owner.Character.MapID != session.Character.MapID {
		return false
	}
	m := s.skillMemberFor(session, live, sink)
	_, valid := s.skillCurrent(m)
	return valid
}

func gmFeedback(sink packetSink, message string) error {
	gmFeedbackTap(sink, message)
	return sink.WritePacket(protocol.PortalTrainingMessage("[QA GM] " + message))
}

func (s *Server) handleGMChat(ctx context.Context, live *liveCharacter, sink packetSink, text string) (bool, error) {
	name, args, arg, handled := parseGMCommand(text)
	if !handled {
		return false, nil
	}
	if name == "" {
		return true, gmFeedback(sink, "Unsupported command or arguments. 輸入 !help 查看指令。")
	}
	if name == "help" && len(args) == 1 {
		if _, ok := gmHelpLines(int(args[0])); !ok {
			return true, gmFeedback(sink, fmt.Sprintf("沒有這一頁，請輸入 !help 1 到 !help %d。", len(gmHelpPages)))
		}
	}
	// Raymond approved QA warps to the reviewed test fields only. The store
	// authorizes and audits; the fenced field transition runs afterwards.
	if name == "warp" && !domain.QAWarpAllowed(int(args[0])) {
		return true, gmFeedback(sink, "!warp is not enabled for that map: QA test fields only.")
	}
	repo, ok := s.repo.(domain.GMRepository)
	if !ok || s.skills == nil {
		return true, gmFeedback(sink, "Commands unavailable.")
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := cloneCombatSession(s.skillSession(live))
	expectedVisit := s.sceneFor(live, sink)
	if expectedVisit == nil {
		return true, domain.ErrStaleOwner
	}
	if !s.gmCurrent(session, live, sink) {
		return true, domain.ErrStaleOwner
	}
	if s.gmBusy(live) {
		return true, gmFeedback(sink, "Finish the active trade, dialogue, transition or settlement first.")
	}
	req := domain.GMCommandRequest{Session: session, OperationID: "gm-native-" + platform.NewID(), Name: name, Args: args, Text: arg}
	if name == "spawn" || name == "clearmobs" {
		if s.gameplay == nil || !domain.ValidGMMobCommand(name, args) {
			return true, qaMobDenied(sink, "spawn/clearmobs require the empty QA field and bounded arguments")
		}
		req.RuntimeID = s.gameplay.bootID
	}
	timeout := 3 * time.Second
	if name == "maxskills" {
		// The first use reads every skill of the job from the WZ files.
		timeout = 20 * time.Second
	}
	call, cancel := context.WithTimeout(ctx, timeout)
	result, err := repo.ExecuteGMCommand(call, req)
	cancel()
	if err != nil && !combatKnownFailure(err) {
		slog.Warn("qa_gm_command_failed", "character", session.CharacterID, "command", name, "args", args, "error", err)
		// Never execute again after an uncertain commit. This lookup uses the
		// SAME operation and authority locks to establish one durable outcome.
		resolve, done := context.WithTimeout(ctx, 3*time.Second)
		var found bool
		result, found, err = repo.ResolveGMCommand(resolve, req)
		done()
		if err == nil && !found {
			return true, gmFeedback(sink, "Command did not commit.")
		}
		if err != nil && !combatKnownFailure(err) {
			return true, err
		}
	}
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return true, err
		}
		if err := gmFeedback(sink, "Denied: no active dedicated QA grant or unsupported state."); err != nil {
			return true, err
		}
		// The launcher matches the denial line exactly; the cause follows it.
		if result.Detail != "" {
			return true, gmFeedback(sink, "Reason: "+result.Detail+".")
		}
		return true, nil
	}
	sceneUnlock, sceneErr := s.lockScenePublication(session.CharacterID)
	if sceneErr != nil {
		return true, sceneErr
	}
	defer sceneUnlock()
	if !s.sceneCurrent(expectedVisit) {
		return true, domain.ErrStaleOwner
	}
	if name == "spawn" || name == "clearmobs" {
		return true, s.publishQAMobCommand(req, result, expectedVisit, sink)
	}
	unlock := s.lockSkillPublication()
	published := false
	defer func() {
		unlock()
		if published && result.Changed {
			s.refreshParty(live)
		}
	}()
	if !s.gmCurrent(session, live, sink) {
		return true, domain.ErrStaleOwner
	}
	// A map change is never applied through this adapter, even from a faulty
	// repository; an authorized !warp moves via warpGameplayConditional below.
	warp := name == "warp" && result.MapChanged && !result.Changed
	if result.MapChanged && !warp || result.Character.ID != session.CharacterID || result.Character.AccountID != session.AccountID || result.Character.WorldID != session.Lease.WorldID || result.Character.MapID != session.Character.MapID || result.Character.Revision < session.Character.Revision {
		return true, domain.ErrStaleOwner
	}
	live.mu.Lock()
	next := cloneCombatSession(live.session)
	if !sameSceneOwner(next, session) {
		live.mu.Unlock()
		return true, domain.ErrStaleOwner
	}
	if result.Changed {
		// Position can advance independently of asset commits. This adapter
		// cannot warp, so retain the latest live position and dirty checkpoint.
		x, y, spawn := next.Character.X, next.Character.Y, next.Character.SpawnPoint
		next.Character = result.Character
		next.Character.X, next.Character.Y, next.Character.SpawnPoint = x, y, spawn
	}
	if !s.updateOnlineOwned(session, next) {
		live.mu.Unlock()
		return true, domain.ErrStaleOwner
	}
	live.session = next
	live.mu.Unlock()
	published = true
	if result.Changed && name == "hide" {
		// The caller's own buff row; gmStanding then hides or shows the body.
		if args[0] == 1 {
			s.publishQADebuff(session, live, sink, next.Character, gmHideBit)
		} else {
			s.publishQABuffReset(session, live, sink, gmHideBit)
		}
	} else if result.Changed && name == "buffreset" {
		s.publishQABuffReset(session, live, sink, int(args[0]), lostStats(session.Character, next.Character)...)
	} else if result.Changed && name == "debuff" {
		s.publishQADebuff(session, live, sink, next.Character, int(args[0]))
	} else if result.Changed {
		if name == "level" || name == "job" {
			if err = sink.WritePacket(protocol.JobAdvancementStats(next.Character)); err != nil {
				return true, err
			}
			if err = sink.WritePacket(protocol.UpdateCombatStats(next.Character)); err != nil {
				return true, err
			}
			if name == "job" && hasBuffStat(session.Character, "SHADOWPARTNER") && !hasBuffStat(next.Character, "SHADOWPARTNER") {
				s.publishQABuffReset(session, live, sink, 26) // the old line's partner was dropped
			}
			s.refreshCharacterLookScene(expectedVisit)
			if name == "job" {
				s.respawnSceneLocked(expectedVisit) // skill publication is held
			}
		} else {
			for _, id := range result.SkillIDs { // !maxskills
				for _, state := range next.Character.Skills {
					if state.ID == id {
						if err = sink.WritePacket(protocol.SkillUpdate(state)); err != nil {
							return true, err
						}
					}
				}
			}
			switch name {
			case "hair", "face", "skin":
				if err = sink.WritePacket(protocol.UpdateLook(next.Character, name)); err != nil {
					return true, err
				}
				s.refreshCharacterLookScene(expectedVisit)
			case "fame":
				if err = sink.WritePacket(protocol.UpdateFame(next.Character.Fame)); err != nil {
					return true, err
				}
			}
			if result.SkillID != 0 {
				for _, state := range next.Character.Skills {
					if state.ID == result.SkillID {
						if err = sink.WritePacket(protocol.SkillUpdate(state)); err != nil {
							return true, err
						}
					}
				}
			}
			if name == "sp" {
				if err = sink.WritePacket(protocol.JobAdvancementStats(next.Character)); err != nil {
					return true, err
				}
			}
			if name == "stats" || name == "ap" {
				if err = sink.WritePacket(protocol.AttributeStats(next.Character)); err != nil {
					return true, err
				}
			}
			vitals := protocol.InventoryVitals(next.Character)
			if name == "heal" || name == "hp" {
				// Like Java, a heal sends HP/MP only: a MAXHP/MAXMP stat
				// change replaces the client's passive-derived maxima with
				// the base wire values (mhpR passives then read lower).
				vitals = protocol.UpdateHPMP(next.Character.HP, next.Character.MP)
			}
			if err = sink.WritePacket(vitals); err != nil {
				return true, err
			}
		}
	} else if name == "heal" {
		// Already full on the server: re-send HP/MP so a client that missed
		// an update shows the durable values again.
		if err = sink.WritePacket(protocol.UpdateHPMP(next.Character.HP, next.Character.MP)); err != nil {
			return true, err
		}
	}
	if result.Changed && name == "hp" && next.Character.HP == 0 {
		// The ordinary death flow: cancel the buffs once this command has
		// released its locks (takeDamage does the same under persistMu).
		go func() {
			live.persistMu.Lock()
			defer live.persistMu.Unlock()
			if err := s.clearDeathBuffs(context.Background(), live, sink); err != nil {
				slog.Warn("death_buff_cancellation_failed", "character", next.Character.ID, "error", err)
			}
		}()
	}
	if warp {
		// After this command's persistMu: the transition takes it itself and
		// re-checks the same session, field and liveness under its guard.
		from, channel, target := session.Character.MapID, session.ChannelID, int(args[0])
		go func() {
			call, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			m, err := s.gameplay.catalog.Map(target)
			if err != nil {
				slog.Warn("qa_warp_failed", "map", target, "error", err)
				return
			}
			entry, ok := m.Spawn()
			if !ok {
				return
			}
			guard := func(current domain.CharacterSession) bool {
				return current.SessionID == session.SessionID && current.Generation == session.Generation && current.ChannelID == channel && current.Character.MapID == from && current.Character.HP > 0
			}
			if err = s.warpGameplayConditional(call, channel, live, sink, m.ID, entry, nil, guard, nil); err != nil {
				slog.Warn("qa_warp_failed", "map", target, "error", err)
			}
		}()
	}
	if name == "alliance" && len(args) == 1 {
		// QA stand-in for NPC 2010009 (Orbis is unreachable without warps):
		// the same durable create command, run after this command's persistMu.
		partner := args[0]
		req := domain.AllianceRequest{Action: domain.AllianceCreate, Name: fmt.Sprintf("QA%d", partner), PartnerID: partner}
		if partner == 0 {
			req = domain.AllianceRequest{Action: domain.AllianceDisband}
		}
		go func() {
			call, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if _, err := s.commitAlliance(call, live, sink, req); err != nil {
				slog.Warn("qa_alliance_create_failed", "partner", partner, "error", err)
			}
		}()
	}
	if name == "mobhold" {
		// Authorized on a QA test field: this field's mobs stop dying and
		// refill each second, so attack-applied statuses can be read.
		s.holdFieldMobs(mapKey{channel: session.ChannelID, mapID: session.Character.MapID}, args[0] == 1)
	}
	if name == "mesos" {
		if err := sink.WritePacket(protocol.UpdateMesos(next.Character.Mesos, true)); err != nil {
			return true, err
		}
	}
	if (name == "petfood" || name == "item") && len(result.Inventory) > 0 {
		if err := sink.WritePacket(protocol.ModifyInventory(result.Inventory)); err != nil {
			return true, err
		}
	}
	if name == "petexpire" && result.PetExpiry != nil {
		for _, pet := range next.Character.Pets {
			if pet.CashID == result.PetExpiry.CashID {
				if err := sink.WritePacket(protocol.UpdatePet(pet)); err != nil {
					return true, err
				}
			}
		}
		return true, gmFeedback(sink, fmt.Sprintf("Committed petexpire: pet serial %d expires at %d (was %d); setup provenance: gm_command.", result.PetExpiry.CashID, result.PetExpiry.ExpiresAt, result.PetExpiry.PreviousExpiresAt))
	}
	if name == "cashexpire" && result.CashExpiry != nil {
		return true, gmFeedback(sink, fmt.Sprintf("Committed cashexpire: locker serial %d expires at %d (was %d); setup provenance: gm_command.", result.CashExpiry.CashID, result.CashExpiry.ExpiresAt, result.CashExpiry.PreviousExpiresAt))
	}
	if name == "whereami" {
		return true, gmFeedback(sink, fmt.Sprintf("CID %d world %d channel %d map %d (%d,%d), level %d job %d.", next.CharacterID, next.Lease.WorldID, next.ChannelID, next.Character.MapID, next.Character.X, next.Character.Y, next.Character.Level, next.Character.Job))
	}
	if name == "help" {
		page := 0
		if len(args) == 1 {
			page = int(args[0])
		}
		lines, _ := gmHelpLines(page)
		return true, gmLines(sink, lines)
	}
	if name == "online" {
		return true, gmLines(sink, s.gmOnlineLines())
	}
	if name == "rates" {
		return true, gmFeedback(sink, fmt.Sprintf("目前倍率：經驗 x%d、楓幣 x%d、掉寶 x%d。", domain.ExpRate(), domain.MesoRate(), domain.DropRate()))
	}
	if domain.GMStandingCommand(name) {
		// After this command's persistMu: a warp takes it itself, and the
		// other effects must not run under one character's lock.
		go s.gmStanding(session, live, sink, req)
		return true, nil
	}
	if name == "effstats" {
		for _, line := range effectiveStatsLines(next.Character, time.Now()) {
			if err := gmFeedback(sink, line); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	return true, gmFeedback(sink, "Committed "+name+"; setup provenance: gm_command.")
}

// publishQABuffReset sends the native reset of one stat (by bit) to the QA
// character and its visible peers, even when no server row held it, so a
// client stuck in a mode (Mechanic Siege) leaves it. A MECH_CHANGE reset also
// carries bit 137. Requires publication.
// lostStats are the buff stats before had and after lacks (a !buffreset of a
// two-state stat removes its whole buff).
func lostStats(before, after domain.Character) []buffstat.Stat {
	kept := map[string]bool{}
	for _, b := range after.SkillBuffs {
		kept[b.Stat] = true
	}
	var out []buffstat.Stat
	seen := map[string]bool{}
	for _, b := range before.SkillBuffs {
		if !kept[b.Stat] && !seen[b.Stat] {
			seen[b.Stat] = true
			out = append(out, buffstat.Stat(b.Stat))
		}
	}
	return out
}

func (s *Server) publishQABuffReset(session domain.CharacterSession, live *liveCharacter, sink packetSink, bit int, also ...buffstat.Stat) {
	spec, ok := buffstat.ByBit(bit)
	if !ok {
		return
	}
	stats := []buffstat.Stat{spec.Name}
	for _, st := range also {
		if other, ok := buffstat.Lookup(st); ok && other.Name != spec.Name {
			stats = append(stats, other.Name)
		}
	}
	if p, err := protocol.TemporaryStatReset(0, stats); err == nil {
		_ = sink.WritePacket(p)
	}
	source := s.skillMemberFor(session, live, sink)
	if source == nil {
		return
	}
	p, err := protocol.TemporaryStatReset(session.CharacterID, stats)
	if err != nil {
		return
	}
	for _, target := range s.skillPeers(session.CharacterID, session.ChannelID, session.Character.MapID) {
		if s.skillVisible(source, target) {
			_ = target.sink.WritePacket(p)
		}
	}
}

// publishQADebuff sends the !debuff status row to the character and its
// visible peers. Requires publication.
func (s *Server) publishQADebuff(session domain.CharacterSession, live *liveCharacter, sink packetSink, c domain.Character, bit int) {
	spec, ok := buffstat.ByBit(bit)
	if !ok {
		return
	}
	now := time.Now()
	var rows []domain.SkillBuff
	for _, b := range c.SkillBuffs {
		if b.Stat == string(spec.Name) && b.ExpiresAt.After(now) {
			rows = append(rows, b)
		}
	}
	if len(rows) == 0 {
		return
	}
	_ = sink.WritePacket(protocol.SkillBuffsAmong(rows, c.SkillBuffs, now))
	source := s.skillMemberFor(session, live, sink)
	if source == nil {
		return
	}
	foreign := protocol.ForeignSkillBuffs(session.CharacterID, rows)
	if foreign == nil {
		return // POISON's remote layout is not reviewed: local only.
	}
	for _, target := range s.skillPeers(session.CharacterID, session.ChannelID, session.Character.MapID) {
		if s.skillVisible(source, target) {
			_ = target.sink.WritePacket(foreign)
		}
	}
}

// effectiveStatsLines is the !effstats echo: the attacker stats Go uses for
// damage (equipment, registered passives and active buffs), then the
// equipment-only view, so QA can see what a passive or buff adds. The MCP
// parses these exact "key value" lines.
func effectiveStatsLines(c domain.Character, now time.Time) []string {
	fields := func(p combat.PlayerStats) string {
		return fmt.Sprintf("str %d dex %d int %d luk %d watk %d matk %d acc %d mastery %d crit %d critdmg %d damr %d bossr %d ignoredef %d elemamp %d shadowpartner %d critdmgmin %d asr %d ter %d avoid %d wdef %d mdef %d speed %d jump %d dodge %d",
			p.STR, p.DEX, p.INT, p.LUK, p.WATK, p.MATK, p.Accuracy, p.Mastery, p.CritRate, p.CritDamage, p.DamageRate, p.BossDamageRate, p.IgnoreDefense, p.ElementAmp, p.ShadowPartner, p.CritDamageMin, p.ASR, p.TER,
			p.Avoid, p.WDEF, p.MDEF, p.Speed, p.Jump, p.DodgeRate)
	}
	total, err := combat.AttackStats(c, now)
	if err != nil {
		return []string{"Effective stats unavailable: " + err.Error() + "."}
	}
	effective := fmt.Sprintf("Effective stats: %s wdef %d mdef %d speed %d jump %d magicacc %d avoid %d magicavoid %d maxhp %d maxmp %d",
		fields(total), total.WDEF, total.MDEF, total.Speed, total.Jump, total.MagicAccuracy, total.Avoid, total.MagicAvoid, c.MaxHP, c.MaxMP)
	if c.MapID == 960010100 {
		if arena, err := skills.BattleSquareMeleeStats(c, now); err == nil {
			effective += fmt.Sprintf(" pvpwdef %d", arena.WDEF)
		}
	}
	if c.VitalsModel == 1 {
		effective += fmt.Sprintf(" basehp %d basemp %d", c.BaseMaxHP, c.BaseMaxMP)
	}
	effective += "."
	base, err := combat.BaseStats(c, now)
	if err != nil {
		return []string{effective}
	}
	return []string{effective, "Equipment only: " + fields(base) + "."}
}

// hasBuffStat reports a stored buff row of stat, expired or not: the reset
// is only sent for a row the job change removed.
func hasBuffStat(c domain.Character, stat string) bool {
	for _, b := range c.SkillBuffs {
		if b.Stat == stat {
			return true
		}
	}
	return false
}
