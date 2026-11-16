package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

type scriptWorld struct {
	// reload serializes reads of the script directory.
	reload sync.Mutex
	mu     sync.Mutex
	engine *scripting.Engine
	active map[int64]*worldScript
	armed  map[int64]scriptDebugArm
}

// The native client only sends NPCTalk for templates with a script string, and
// WZ shop NPCs such as Amherst 11100 carry none; register catalog shops too so
// openNativeNPCShop is reachable instead of a local speech bubble.
func (s *Server) npcScriptability(npcs map[int32]content.Life) [][]byte {
	ids := make([]int, 0, len(npcs))
	seen := make(map[int]bool)
	for _, npc := range npcs {
		if seen[npc.TemplateID] {
			continue
		}
		_, shopErr := content.NPCShopForNPC(npc.TemplateID)
		scripted := false
		if s.scripts != nil {
			_, err := scripts.Source(fmt.Sprintf("npc/%d.lua", npc.TemplateID))
			scripted = err == nil
		}
		if shopErr == nil || scripted {
			ids = append(ids, npc.TemplateID)
			seen[npc.TemplateID] = true
		}
	}
	sort.Ints(ids)
	var packets [][]byte
	for len(ids) > 0 {
		count := min(len(ids), 255)
		entries := make([]protocol.NPCScript, count)
		for i, id := range ids[:count] {
			entries[i] = protocol.NPCScript{TemplateID: id, Name: "對話"}
		}
		packets = append(packets, protocol.NPCScriptable(entries))
		ids = ids[count:]
	}
	return packets
}

type scriptDebugArm struct {
	identity scripting.Identity
	options  scripting.StartOptions
}

type worldScript struct {
	mu       sync.Mutex
	server   *Server
	live     *liveCharacter
	sink     packetSink
	npcID    int
	kind     string
	objectID int32
	identity scripting.Identity
	session  *scripting.Session
	// portal is the source of a portal invocation, mapID its map; the portal
	// host revalidates the character's position before it delegates a command.
	portal content.Portal
	mapID  int
	// remoteMap is set for a conversation a portal script opened (Legacy
	// pi.openNpc): there is no NPC object, so its context is the map the
	// character stood on when it opened.
	remoteMap int
	// openNPC is the NPC this conversation asked to talk to next (Legacy
	// cm.openNpc); it opens once this script has ended. chain counts how many
	// conversations opened one another, so two sources cannot loop forever.
	openNPC int
	chain   int
	// scene marks a cutscene (scripts/scene); sceneLocked is true while it
	// holds the player's controls.
	scene, sceneLocked bool
}

func newScriptWorld() (*scriptWorld, error) {
	engine, err := scripting.New(scripting.Options{})
	if err != nil {
		return nil, err
	}
	manifest, err := scripts.Manifest()
	if err != nil {
		return nil, err
	}
	for name := range manifest {
		source, e := scripts.Source(name)
		if e != nil {
			return nil, e
		}
		if _, e = engine.Register(name, source); e != nil {
			return nil, e
		}
	}
	return &scriptWorld{engine: engine, active: make(map[int64]*worldScript), armed: make(map[int64]scriptDebugArm)}, nil
}

func (s *Server) cancelNPCScript(live *liveCharacter) {
	s.cancelNPCScriptState(live, true)
}

func (s *Server) cancelNPCScriptState(live *liveCharacter, clearArm bool) {
	if clearArm {
		s.invalidateMapEntry(live)
	}
	if s.scripts == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	identity := scripting.Identity{SessionID: live.session.SessionID, CharacterID: id, Generation: live.session.Generation}
	live.mu.Unlock()
	s.scripts.mu.Lock()
	if arm, ok := s.scripts.armed[id]; clearArm && ok && arm.identity == identity {
		delete(s.scripts.armed, id)
	}
	active := s.scripts.active[id]
	if active != nil && active.live == live {
		delete(s.scripts.active, id)
	} else {
		active = nil
	}
	s.scripts.mu.Unlock()
	if active != nil {
		active.mu.Lock()
		if active.session != nil {
			active.session.Cancel()
		}
		active.mu.Unlock()
	}
}

func (s *Server) startNPCScript(ctx context.Context, channel int, live *liveCharacter, sink packetSink, objectID int32) (bool, error) {
	return s.startScriptAtNPC(ctx, channel, live, sink, objectID, "")
}

func (s *Server) startScriptAtNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, objectID int32, name string) (bool, error) {
	if s.scripts == nil || s.gameplay == nil {
		return false, nil
	}
	live.mu.Lock()
	snapshot := live.session
	live.mu.Unlock()
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{channel: channel, mapID: snapshot.Character.MapID}]
	npcID := 0
	if room != nil {
		if npc, ok := room.npcs[objectID]; ok && npcReach(snapshot.Character.X, snapshot.Character.Y, npc.X, npc.Y) {
			npcID = npc.TemplateID
		}
	}
	s.gameplay.mu.Unlock()
	if npcID == 0 {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if s.tradeActive(live) {
		s.npcGMNotice(live, sink, npcID, "未處理(交易中不能對話)")
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if name == "" {
		name = fmt.Sprintf("npc/%d.lua", npcID)
	}
	if _, err := scripts.Source(name); err != nil {
		if reason, ok := scripts.LegacyNPCPendingReason(npcID); ok {
			s.npcGMNotice(live, sink, npcID, "未處理(Legacy npc/%d.js 尚未移植: %s)", npcID, reason)
		} else {
			s.npcGMNotice(live, sink, npcID, "未處理(伺服器沒有 %s 腳本)", name)
		}
		return false, nil
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	a := &worldScript{server: s, live: live, sink: sink, kind: "npc", npcID: npcID, objectID: objectID, identity: scripting.Identity{SessionID: snapshot.SessionID, CharacterID: snapshot.CharacterID, Generation: snapshot.Generation}}
	s.scripts.mu.Lock()
	var options scripting.StartOptions
	if arm, ok := s.scripts.armed[snapshot.CharacterID]; ok && arm.identity == a.identity {
		options = arm.options
	}
	delete(s.scripts.armed, snapshot.CharacterID)
	s.scripts.active[snapshot.CharacterID] = a
	s.scripts.mu.Unlock()
	options.Host = a
	a.mu.Lock()
	defer a.mu.Unlock()
	var event scripting.Event
	var err error
	a.gmNotice("%s 開始 (物件 %d)", name, objectID)
	a.session, event, err = s.scripts.engine.Start(ctx, a.identity, name, options)
	if err != nil {
		return true, a.fail(err)
	}
	return true, a.deliver(event)
}

func (a *worldScript) fail(err error) error {
	if a.session != nil {
		a.session.Cancel()
	}
	a.server.scripts.mu.Lock()
	if a.server.scripts.active[a.identity.CharacterID] == a {
		delete(a.server.scripts.active, a.identity.CharacterID)
	}
	a.server.scripts.mu.Unlock()
	a.sceneRelease()
	slog.Warn("lua_dialog_failed", "character", a.identity.CharacterID, "npc", a.npcID, "error", err)
	if !errors.Is(err, scripting.ErrCancelled) {
		if a.kind == "portal" {
			a.gmNotice("腳本中止 錯誤:%v", err)
		} else {
			a.gmNotice("對話中止 錯誤:%v", err)
		}
	}
	return a.sink.WritePacket(protocol.EnableActions())
}

func (a *worldScript) deliver(event scripting.Event) error {
	a.gmEvent(event)
	if event.Kind == "prompt" && event.Prompt != nil {
		if a.kind == "portal" || a.kind == "map" {
			return a.fail(errors.New("map and portal scripts cannot show NPC prompts"))
		}
		p := event.Prompt
		if a.scene {
			if packet, ok := a.scenePrompt(p); ok {
				if packet == nil {
					return a.fail(protocol.ErrMalformed)
				}
				return a.sink.WritePacket(packet)
			}
		}
		choices := make([]protocol.NPCMenuChoice, len(p.Choices))
		for i, c := range p.Choices {
			choices[i] = protocol.NPCMenuChoice{ID: c.ID, Text: c.Text}
		}
		packet := protocol.NPCPromptSpeaker(a.npcID, p.Kind, p.Text, choices, p.AllowBack, p.AllowNext, p.Speaker)
		if packet == nil {
			return a.fail(protocol.ErrMalformed)
		}
		return a.sink.WritePacket(packet)
	}
	if event.Kind == "done" {
		a.sceneRelease()
		a.server.scripts.mu.Lock()
		if a.server.scripts.active[a.identity.CharacterID] == a {
			delete(a.server.scripts.active, a.identity.CharacterID)
		}
		a.server.scripts.mu.Unlock()
		if a.openNPC != 0 && a.kind == "npc" && a.chain < 8 {
			a.live.mu.Lock()
			channel := a.live.session.ChannelID
			a.live.mu.Unlock()
			return a.server.openChainedNPC(context.Background(), channel, a.live, a.sink, a.openNPC, a.chain+1)
		}
		return a.sink.WritePacket(protocol.EnableActions())
	}
	// Debugger pauses leave this character's script suspended. No server-wide
	// lock, DB transaction or other character's coroutine stays blocked.
	return nil
}

func (s *Server) respondNPCScript(ctx context.Context, live *liveCharacter, r *protocol.Reader) (bool, error) {
	if s.scripts == nil {
		return false, nil
	}
	live.mu.Lock()
	id := live.session.CharacterID
	generation := live.session.Generation
	live.mu.Unlock()
	s.scripts.mu.Lock()
	a := s.scripts.active[id]
	s.scripts.mu.Unlock()
	if a == nil || a.live != live {
		return false, nil
	}
	response, err := protocol.ReadNPCResponse(r)
	if err != nil {
		return true, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil || generation != a.identity.Generation {
		return true, a.fail(scripting.ErrIdentity)
	}
	event, err := a.session.CurrentEvent(a.identity)
	if err != nil {
		return true, a.fail(err)
	}
	if event.Kind != "prompt" || event.Prompt == nil {
		return true, a.sink.WritePacket(protocol.EnableActions())
	}
	if response.Kind != event.Prompt.Kind && !response.Cancel {
		a.gmNotice("第%d行 回應類型不符(等待%s，收到%s)，忽略", event.Line, event.Prompt.Kind, response.Kind)
		return true, a.sink.WritePacket(protocol.EnableActions())
	}
	a.gmNotice("第%d行 回應:%s", event.Line, gmResponse(event.Prompt.Kind, response, event.Prompt))
	if response.Cancel {
		return true, a.fail(scripting.ErrCancelled)
	}
	event, err = a.session.Respond(ctx, scripting.Response{Identity: a.identity, PromptID: event.Prompt.ID, Kind: response.Kind, Bool: response.Bool, Choice: response.Choice, Text: response.Text, Back: response.Back, Finish: response.Kind == "say" && response.Bool && !event.Prompt.AllowNext})
	if err != nil {
		return true, a.fail(err)
	}
	return true, a.deliver(event)
}

func (a *worldScript) Execute(ctx context.Context, id scripting.Identity, command scripting.Command) (scripting.CommandResult, error) {
	result, err := a.executeCommand(ctx, id, command)
	a.gmCommand(command, result, err)
	return result, err
}

func (a *worldScript) executeCommand(ctx context.Context, id scripting.Identity, command scripting.Command) (scripting.CommandResult, error) {
	a.live.mu.Lock()
	snapshot := a.live.session
	a.live.mu.Unlock()
	if id != a.identity || id.SessionID != snapshot.SessionID || id.Generation != snapshot.Generation || id.CharacterID != snapshot.CharacterID {
		return scripting.CommandResult{}, scripting.ErrIdentity
	}
	// A debugger pause can outlive the original interaction position. Recheck
	// the authoritative NPC object before every effect, rather than letting a
	// resumed conversation open a remote shop or grant a reward from another map.
	switch command.Kind {
	case "quest_start", "quest_complete", "quest_forfeit", "exchange", "change_look", "warp", "open_storage", "open_merchant_bank", "open_shop", "advance_warrior", "advance_first_job", "advance_second_job", "depart_island", "take_taxi",
		"guild_prompt", "guild_disband", "guild_capacity", "alliance_create", "alliance_capacity", "alliance_disband",
		"profession_learn", "profession_forget", "open_crafting", "event_start", "event_finish", "event_leave", "event_set", "event_effect", "henesys_finish",
		"transit_status", "transit_board", "transit_leave", "subway_challenge", "hak_board":
		if !a.hasNPCContext(snapshot) {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
	}
	result := func(value any) (scripting.CommandResult, error) { return scripting.CommandResult{Value: value}, nil }
	if strings.HasPrefix(command.Kind, "scene_") {
		return a.sceneCommand(snapshot, command)
	}
	switch command.Kind {
	case "map_id":
		return result(int64(snapshot.Character.MapID))
	case "level":
		return result(int64(snapshot.Character.Level))
	case "job":
		return result(int64(snapshot.Character.Job))
	case "hp":
		return result(int64(snapshot.Character.HP))
	case "gender":
		return result(int64(snapshot.Character.Gender))
	case "hair":
		return result(int64(snapshot.Character.Hair))
	case "face":
		return result(int64(snapshot.Character.Face))
	case "mesos":
		return result(snapshot.Character.Mesos)
	case "name":
		return result(snapshot.Character.Name)
	case "message":
		if len(command.Ints) > 1 || command.Text == "" || len(command.Text) > 512 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		// Every Legacy kind is shown as the type-5 chat notice, as for portals.
		return scripting.CommandResult{}, a.sink.WritePacket(protocol.PortalTrainingMessage(command.Text))
	case "stat":
		c := snapshot.Character
		stats := map[string]int{"fame": c.Fame, "sp": c.SP, "ap": c.AP, "str": c.STR, "dex": c.DEX, "int": c.INT, "luk": c.LUK,
			"skill_levels": skillLevels(c), "hp": c.HP, "mp": c.MP, "max_hp": c.MaxHP, "max_mp": c.MaxMP, "skin": c.Skin, "sub_job": c.SubJob, "channel": snapshot.ChannelID}
		value, ok := stats[command.Text]
		if !ok || len(command.Ints) != 0 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return result(int64(value))
	case "has_equipped":
		if len(command.Ints) != 1 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		for _, e := range snapshot.Character.Equipment {
			if e.Slot < 0 && int64(e.ItemID) == command.Ints[0] {
				return result(true)
			}
		}
		return result(false)
	case "player_count":
		if len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] > 999999999 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return result(int64(a.server.mapPlayerCount(snapshot.ChannelID, int(command.Ints[0]))))
	case "monster_count":
		return result(int64(a.server.mapMonsterCount(snapshot.ChannelID, snapshot.Character.MapID)))
	case "party":
		// id 0 means the character is in no party.
		value := map[string]any{"id": int64(0), "leader": false, "size": int64(0)}
		if a.server.parties != nil {
			a.server.parties.mu.Lock()
			if peer := a.server.parties.peers[snapshot.CharacterID]; peer != nil && peer.party.ID != 0 {
				value = map[string]any{"id": peer.party.ID, "leader": peer.party.LeaderID == snapshot.CharacterID, "size": int64(len(peer.party.Members))}
			}
			a.server.parties.mu.Unlock()
		}
		return result(value)
	case "character_id":
		return result(snapshot.CharacterID)
	case "event_status", "event_start", "event_get", "event_set", "event_finish", "event_leave", "event_effect", "event_areas", "event_party_exp":
		return a.eventCommand(ctx, snapshot, command)
	case "transit_status", "transit_board", "transit_leave":
		return a.transitCommand(ctx, snapshot, command)
	case "subway_challenge":
		return a.subwayChallenge(ctx, snapshot, command)
	case "henesys_finish":
		inst := a.server.eventOf(snapshot.CharacterID)
		if a.npcID != 1012114 || snapshot.Character.MapID != content.HenesysPQMap || inst == nil || inst.moon == nil {
			return result("unavailable")
		}
		if a.server.partySnapshotOf(snapshot.CharacterID).LeaderID != snapshot.CharacterID {
			return result("not_leader")
		}
		return result(a.server.finishMoonRabbit(ctx, inst, snapshot))
	case "hak_board":
		return a.boardHak(ctx, snapshot, command)
	case "open_npc":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 9999999 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		a.openNPC = int(command.Ints[0])
		return scripting.CommandResult{}, nil
	case "map_message":
		if len(command.Ints) > 1 || command.Text == "" || len(command.Text) > 512 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		// Legacy mapMessage: the notice line goes to everyone on this map.
		a.server.broadcastInventoryPacket(snapshot.ChannelID, snapshot.Character.MapID, protocol.PortalTrainingMessage(command.Text))
		return scripting.CommandResult{}, nil
	case "item_count":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 9999999 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		quantity := 0
		for _, item := range snapshot.Character.Items {
			if int64(item.TemplateID) == command.Ints[0] {
				quantity += item.Quantity
			}
		}
		return result(int64(quantity))
	case "can_hold":
		if len(command.Ints) != 2 || command.Ints[0] < 1 || command.Ints[0] > 9999999 || command.Ints[1] < 1 || command.Ints[1] > 30000 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return result(canHoldItem(snapshot.Character, int(command.Ints[0]), int(command.Ints[1])))
	case "depart_island":
		if len(command.Ints) != 0 || command.Text != "" {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return a.departIsland(ctx, command)
	case "take_taxi":
		return a.takeTaxi(ctx, command)
	case "style_filter":
		return styleFilter(command)
	case "change_look":
		return a.changeLook(ctx, command)
	case "quest_state":
		if len(command.Ints) != 1 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		repo, ok := a.server.repo.(domain.QuestRepository)
		if !ok {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		states, err := repo.QuestStates(ctx, snapshot)
		if err != nil {
			return scripting.CommandResult{}, err
		}
		for _, state := range states {
			if state.ID == int(command.Ints[0]) {
				return result(int64(state.State))
			}
		}
		return result(int64(0))
	case "quest_data":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 2147483647 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		repo, ok := a.server.repo.(domain.ScriptQuestRepository)
		if !ok {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		value, err := repo.ReadScriptQuestData(ctx, snapshot, int(command.Ints[0]))
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return result(value.Data)
	case "quest_start", "quest_complete", "quest_forfeit":
		if len(command.Ints) != 1 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		action := map[string]string{"quest_start": "start", "quest_complete": "complete", "quest_forfeit": "forfeit"}[command.Kind]
		_, err := a.server.changeQuest(ctx, a.live, a.sink, domain.QuestRequest{QuestID: int(command.Ints[0]), NPCID: a.npcID, Action: action, OperationID: command.ID, Script: command.Script})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return result(true)
	case "read_log":
		repo, ok := a.server.repo.(domain.ScriptRepository)
		if !ok || command.Log == nil {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		values, err := repo.ReadScriptLogs(ctx, snapshot, []domain.ScriptLogKey{*command.Log})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return result(values[0].Value)
	case "exchange":
		repo, ok := a.server.repo.(domain.ScriptRepository)
		if !ok || command.Exchange == nil {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		growth := command.Exchange.Growth
		if growth != nil && !scripts.LegacyNPC(command.Script) {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		if growth != nil && !domain.ValidScriptGrowth(*growth) {
			// A job or amount the source computed that cannot exist.
			return result(map[string]any{"ok": false, "reason": "conditions_not_met"})
		}
		if growth != nil {
			// A style the game data lacks cannot be drawn by any client.
			root := os.Getenv("MAPLEGO_WZ_ROOT")
			if growth.Hair != 0 && !content.StyleExists(root, growth.Hair) || growth.Face != 0 && !content.StyleExists(root, growth.Face) {
				return result(map[string]any{"ok": false, "reason": "no_style"})
			}
		}
		progress := command.Exchange.EXP != 0 || len(command.Exchange.Quests) > 0 || growth != nil
		if progress && !strings.HasPrefix(command.Script, "quest/") && !scripts.LegacyPortal(command.Script) && !scripts.LegacyNPC(command.Script) {
			// Forced quest status and experience belong to reviewed quest
			// sources and generated Legacy portal and NPC sources.
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		if a.server.tradeActive(a.live) {
			return result(map[string]any{"ok": false, "reason": "trade_active"})
		}
		a.live.persistMu.Lock()
		defer a.live.persistMu.Unlock()
		a.live.mu.Lock()
		snapshot = a.live.session
		snapshot.Lease = a.server.currentLease()
		a.live.mu.Unlock()
		request := *command.Exchange
		request.Session = snapshot
		request.OperationID = command.ID
		value, err := repo.ExecuteScriptExchange(ctx, request)
		if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrAlreadyExists) || growth != nil && errors.Is(err, domain.ErrForbidden) {
			return result(map[string]any{"ok": false, "reason": "conditions_not_met"})
		}
		if err != nil {
			return scripting.CommandResult{}, err
		}
		if progress {
			a.server.applyProgressResult(a.live, value.Character)
		} else {
			a.server.applyInventoryResult(a.live, value.Character)
		}
		if len(value.Changes) > 0 {
			if err = a.sink.WritePacket(protocol.ModifyInventory(value.Changes)); err != nil {
				return scripting.CommandResult{}, err
			}
		}
		if err = a.sink.WritePacket(protocol.UpdateMesos(value.Character.Mesos, true)); err != nil {
			return scripting.CommandResult{}, err
		}
		if progress && !value.Replayed {
			for _, quest := range value.Quests {
				packet := protocol.QuestUpdate(quest)
				for _, force := range request.Quests {
					// Legacy shows a forced start's custom data as its progress string.
					if force.QuestID == quest.ID && force.Data != nil && quest.State == 1 {
						packet = protocol.QuestRecord(quest.ID, *force.Data)
					}
				}
				if err = a.sink.WritePacket(packet); err != nil {
					return scripting.CommandResult{}, err
				}
			}
			if err = a.sink.WritePacket(protocol.UpdateCombatStats(value.Character)); err != nil {
				return scripting.CommandResult{}, err
			}
			if growth != nil {
				if err = a.publishGrowth(*growth, value); err != nil {
					return scripting.CommandResult{}, err
				}
			}
			a.live.mu.Lock()
			current := a.live.session
			a.live.mu.Unlock()
			if err = a.server.refreshQuestDrops(ctx, current, a.sink); err != nil {
				return scripting.CommandResult{}, err
			}
		}
		return result(map[string]any{"ok": true})
	case "advance_warrior":
		if len(command.Ints) != 0 || command.Text != "" {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return a.advanceWarrior(ctx, command)
	case "advance_second_job":
		return a.advanceSecondJob(ctx, command)
	case "advance_first_job":
		if len(command.Ints) != 1 || command.Text != "" || command.Ints[0] < 100 || command.Ints[0] > 3512 || !domain.ValidJob(int(command.Ints[0])) {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return a.advanceFirstJob(ctx, command, int(command.Ints[0]))
	case "warp":
		m, portal, err := scriptDestination(a.server.gameplay.catalog, command)
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return scripting.CommandResult{}, a.server.warpGameplay(ctx, snapshot.ChannelID, a.live, a.sink, m.ID, portal)
	case "open_storage":
		return scripting.CommandResult{}, a.server.openStorage(ctx, a.live, a.sink, a.npcID)
	case "open_merchant_bank":
		return scripting.CommandResult{}, a.server.openMerchantBank(ctx, a.live, a.sink, a.npcID)
	case "open_parcel":
		return scripting.CommandResult{}, a.server.openParcel(ctx, a.live, a.sink, a.npcID, false)
	case "guild_rank", "guild_prompt", "guild_disband", "guild_capacity", "guild_capacity_limit", "alliance_rank", "alliance_create", "alliance_capacity", "alliance_disband":
		return a.server.guildScriptCommand(ctx, a.live, a.sink, snapshot, command)
	case "profession_level", "profession_learn", "profession_forget", "open_crafting":
		return a.professionScriptCommand(ctx, snapshot, command)
	case "open_shop":
		if len(command.Ints) != 1 || command.Ints[0] < 1 || command.Ints[0] > 2147483647 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return scripting.CommandResult{}, a.server.openNPCShop(ctx, a.live, a.sink, a.npcID, int(command.Ints[0]))
	default:
		return scripting.CommandResult{}, fmt.Errorf("unsupported Lua host command %q", command.Kind)
	}
}

// publishGrowth shows the client what a ScriptGrowth changed. The job, skill
// points and experience are already in the live character.
func (a *worldScript) publishGrowth(g domain.ScriptGrowth, value domain.ScriptExchangeResult) error {
	a.live.mu.Lock()
	c := &a.live.session.Character
	if value.Character.Revision == c.Revision {
		c.Fame, c.Hair, c.Face, c.Skin = value.Character.Fame, value.Character.Hair, value.Character.Face, value.Character.Skin
		c.SubJob, c.SPBooks = value.Character.SubJob, value.Character.SPBooks
		c.Skills = value.Character.Skills
	}
	current := *c
	a.live.mu.Unlock()
	if g.Job != 0 {
		if err := a.sink.WritePacket(protocol.JobAdvancementStats(current)); err != nil {
			return err
		}
	}
	if g.Fame != 0 {
		if err := a.sink.WritePacket(protocol.UpdateFame(current.Fame)); err != nil {
			return err
		}
	}
	for _, skill := range value.Skills {
		if err := a.sink.WritePacket(protocol.SkillUpdate(skill)); err != nil {
			return err
		}
	}
	if g.Skin != nil {
		if err := a.sink.WritePacket(protocol.UpdateSkin(current.Skin)); err != nil {
			return err
		}
	}
	if g.Hair != 0 || g.Face != 0 {
		if err := a.sink.WritePacket(protocol.UpdateLookStats(current, g.Face != 0, g.Hair != 0)); err != nil {
			return err
		}
	}
	if g.Hair != 0 || g.Face != 0 || g.Skin != nil || g.Job != 0 {
		a.server.refreshCharacterLook(a.live)
	}
	return nil
}

// skillLevels is Legacy getAllSkillLevels: the skill points the character has
// spent, leaving out the beginner books.
func skillLevels(c domain.Character) int {
	total := 0
	for _, s := range c.Skills {
		if book := s.ID / 10000; book%1000 != 0 && book != 1 {
			total += s.Level
		}
	}
	return total
}

func (a *worldScript) hasNPCContext(snapshot domain.CharacterSession) bool {
	if a.server.gameplay == nil {
		return false
	}
	switch {
	case a.kind == "portal":
		// Reached only through portalScriptHost, which has just checked the
		// character still stands at the source portal.
		return true
	case a.remoteMap != 0:
		return snapshot.Character.MapID == a.remoteMap && snapshot.Character.HP > 0
	}
	a.server.gameplay.mu.Lock()
	defer a.server.gameplay.mu.Unlock()
	room := a.server.gameplay.rooms[mapKey{channel: snapshot.ChannelID, mapID: snapshot.Character.MapID}]
	if room == nil {
		return false
	}
	npc, ok := room.npcs[a.objectID]
	return ok && npc.TemplateID == a.npcID && npcReach(snapshot.Character.X, snapshot.Character.Y, npc.X, npc.Y)
}

// npcReach reports whether a character may talk to an NPC from where it
// stands. The native client lets a player click any NPC it draws, wherever the
// character is on the screen: the camera stops at the map edge, so the two can
// be a whole screen apart (Ardentmill's alchemist is 476 pixels from a player
// standing at the smithing anvil). The reach is therefore the largest screen
// the client offers, 1920x1080, in each direction; it only refuses a request
// about an NPC the client could not have drawn.
func npcReach(px, py, nx, ny int) bool {
	dx, dy := px-nx, py-ny
	return dx >= -1920 && dx <= 1920 && dy >= -1080 && dy <= 1080
}

// canHoldItem is Legacy MapleInventoryManipulator.checkSpace for one grant.
func canHoldItem(c domain.Character, itemID, quantity int) bool {
	def, err := content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), itemID)
	if err != nil || def.Cash || def.InvType < 1 || def.InvType > 4 {
		return false
	}
	used := 0
	space := 0
	if def.InvType == 1 {
		for _, e := range c.Equipment {
			if e.Slot > 0 {
				used++
			}
		}
		return c.Capacity(1)-used >= quantity
	}
	for _, item := range c.Items {
		if item.InvType != def.InvType {
			continue
		}
		used++
		if item.TemplateID == itemID && item.Quantity < def.SlotMax {
			space += def.SlotMax - item.Quantity
		}
	}
	return space+(c.Capacity(def.InvType)-used)*def.SlotMax >= quantity
}
