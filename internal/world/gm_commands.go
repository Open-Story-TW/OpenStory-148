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
	case "heal", "whereami", "effstats", "clearmobs", "help": // !effstats: read-only server stat view
		want = 0
	case "level", "job", "warp", "buffreset", "hp":
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

// gmHelpLines is the !help reply. It names every command parseGMChat accepts
// (a test checks this), grouped so the list fits the chat window.
var gmHelpLines = []string{
	"GM 指令只作用在自己身上，參數都是數字，[ ] 內可省略。",
	"角色：!heal 補滿、!hp <值>、!level <等級>（只能升）、!job <職業代碼>、!stats <力> <敏> <智> <幸>、!vitals <最大HP> <最大MP>",
	"技能：!skill <技能代碼> <等級>（不扣 SP）、!debuff <狀態位元> [秒數]、!buffreset <狀態位元>",
	"物品：!item <道具代碼> [數量]、!mesos <楓幣>、!petfood <數量>、!petexpire <寵物序號> <秒>、!cashexpire <保管箱序號> <秒>",
	"地圖：!warp <地圖代碼>（僅測試地圖）、!spawn <怪物代碼> <數量> [X]、!clearmobs、!mobhold <0|1>（僅測試地圖）",
	"其他：!alliance <對方角色ID>（0 為解散）、!whereami 目前位置、!effstats 伺服器計算的攻擊數值、!help",
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
	return sink.WritePacket(protocol.PortalTrainingMessage("[QA GM] " + message))
}

func (s *Server) handleGMChat(ctx context.Context, live *liveCharacter, sink packetSink, text string) (bool, error) {
	name, args, handled := parseGMChat(text)
	if !handled {
		return false, nil
	}
	if name == "" {
		return true, gmFeedback(sink, "Unsupported command or arguments.")
	}
	// Raymond approved QA warps to the reviewed test fields only. The store
	// authorizes and audits; the fenced field transition runs afterwards.
	if name == "warp" && domain.QAWarpMaps[int(args[0])] == "" {
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
	req := domain.GMCommandRequest{Session: session, OperationID: "gm-native-" + platform.NewID(), Name: name, Args: args}
	if name == "spawn" || name == "clearmobs" {
		if s.gameplay == nil || !domain.ValidQAMobCommand(name, args, session.Character.MapID) {
			return true, qaMobDenied(sink, "spawn/clearmobs require the empty QA field and bounded arguments")
		}
		req.RuntimeID = s.gameplay.bootID
	}
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
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
	if result.Changed && name == "buffreset" {
		s.publishQABuffReset(session, live, sink, int(args[0]))
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
			s.refreshCharacterLookScene(expectedVisit)
		} else {
			if result.SkillID != 0 {
				for _, state := range next.Character.Skills {
					if state.ID == result.SkillID {
						if err = sink.WritePacket(protocol.SkillUpdate(state)); err != nil {
							return true, err
						}
					}
				}
			}
			if name == "stats" {
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
		for _, line := range gmHelpLines {
			if err := gmFeedback(sink, line); err != nil {
				return true, err
			}
		}
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
func (s *Server) publishQABuffReset(session domain.CharacterSession, live *liveCharacter, sink packetSink, bit int) {
	spec, ok := buffstat.ByBit(bit)
	if !ok {
		return
	}
	if p, err := protocol.TemporaryStatReset(0, []buffstat.Stat{spec.Name}); err == nil {
		_ = sink.WritePacket(p)
	}
	source := s.skillMemberFor(session, live, sink)
	if source == nil {
		return
	}
	p, err := protocol.TemporaryStatReset(session.CharacterID, []buffstat.Stat{spec.Name})
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
		return fmt.Sprintf("str %d dex %d int %d luk %d watk %d matk %d acc %d mastery %d crit %d critdmg %d damr %d bossr %d ignoredef %d elemamp %d shadowpartner %d",
			p.STR, p.DEX, p.INT, p.LUK, p.WATK, p.MATK, p.Accuracy, p.Mastery, p.CritRate, p.CritDamage, p.DamageRate, p.BossDamageRate, p.IgnoreDefense, p.ElementAmp, p.ShadowPartner)
	}
	total, err := combat.AttackStats(c, now)
	if err != nil {
		return []string{"Effective stats unavailable: " + err.Error() + "."}
	}
	base, err := combat.BaseStats(c, now)
	if err != nil {
		return []string{"Effective stats: " + fields(total) + "."}
	}
	return []string{"Effective stats: " + fields(total) + ".", "Equipment only: " + fields(base) + "."}
}
