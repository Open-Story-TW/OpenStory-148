package world

import (
	"context"
	"errors"
	"strings"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/persistence"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

// Cutscene scripts (scripts/scene) are conversations with extra abilities:
// they lock the controls, walk the character, wait, and let the player or
// another NPC speak. Only the reviewed names below run, each on its own maps,
// started by the map's onUserEnter name or by a portal's script name.
var entrySceneScripts = map[string][]int{
	"PTtutor000": {915000000}, "PTtutor300": {915000300}, "PTtutor500": {915000500}, "PTjob1": {150000000},
}
var portalSceneScripts = map[string][]int{
	"PTtutor200_0": {915000200}, "PTtutor300_0": {915000300}, "PTtutor300_2": {915000300, 915000301}, "PTtutor400_0": {915000400},
}

// sceneScriptName is the source a reviewed cutscene name runs on this map.
func sceneScriptName(table map[string][]int, script string, mapID int) string {
	for _, id := range table[script] {
		if id == mapID {
			name := "scene/" + script + ".lua"
			if _, err := scripts.Source(name); err == nil {
				return name
			}
		}
	}
	return ""
}

func isSceneScript(name string) bool { return strings.HasPrefix(name, "scene/") }

// startScene runs a cutscene bound to the map the character stands on.
func (s *Server) startScene(ctx context.Context, channel int, live *liveCharacter, sink packetSink, name, origin string) error {
	live.mu.Lock()
	snapshot := live.session
	live.mu.Unlock()
	if s.scripts == nil || snapshot.ChannelID != channel || snapshot.Character.HP <= 0 || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	a := &worldScript{server: s, live: live, sink: sink, kind: "npc", scene: true, npcID: persistence.PhantomSceneNPC, remoteMap: snapshot.Character.MapID,
		identity: scripting.Identity{SessionID: snapshot.SessionID, CharacterID: snapshot.CharacterID, Generation: snapshot.Generation}}
	s.scripts.mu.Lock()
	s.scripts.active[snapshot.CharacterID] = a
	s.scripts.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gmNotice("%s 開始 (%s)", name, origin)
	session, event, err := s.scripts.engine.Start(ctx, a.identity, name, scripting.StartOptions{Host: a, API: "scene"})
	a.session = session
	if err != nil {
		return a.fail(err)
	}
	return a.deliver(event)
}

// sceneRelease gives the controls back when a cutscene ends or breaks.
func (a *worldScript) sceneRelease() {
	if a.sceneLocked {
		a.sceneLocked = false
		_ = a.sink.WritePacket(protocol.SceneUILock(false))
	}
}

// scenePrompt writes the prompts only a cutscene has; ok is false for the
// ordinary ones.
func (a *worldScript) scenePrompt(p *scripting.Prompt) (packet []byte, ok bool) {
	switch {
	case p.Kind == "wait":
		return protocol.SceneDelay(p.Wait), true
	case p.Kind == "image":
		paths := make([]string, len(p.Choices))
		for i, c := range p.Choices {
			paths[i] = c.Text
		}
		return protocol.SceneImages(a.npcID, paths), true
	case p.Kind == "say" && (p.SpeakerNPC != 0 || p.Speaker == 3):
		return protocol.SceneSay(a.npcID, p.SpeakerNPC, p.Text, p.AllowBack, p.AllowNext), true
	}
	return nil, false
}

// sceneCommand runs scene:direct(op, ...).
func (a *worldScript) sceneCommand(snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	if !a.scene || !a.hasNPCContext(snapshot) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	n, text := command.Ints, command.Text
	arg := func(i int, low, high int64) (int, bool) {
		if i >= len(n) || n[i] < low || n[i] > high {
			return 0, false
		}
		return int(n[i]), true
	}
	flag := func() (bool, bool) {
		on, ok := arg(0, 0, 1)
		return on == 1, ok && len(n) == 1 && text == ""
	}
	named := len(n) == 0 && text != ""
	var packet []byte
	switch strings.TrimPrefix(command.Kind, "scene_") {
	case "lock": // lock(1|0): take or return the controls
		on, ok := flag()
		if !ok {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		a.sceneLocked = on
		packet = protocol.SceneUILock(on)
	case "alone": // alone(1|0): hide the other players
		on, ok := flag()
		if !ok {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		packet = protocol.SceneStandAlone(on)
	case "walk": // walk(0 stop | 1 left | 2 right)
		direction, ok := arg(0, 0, 2)
		if !ok || len(n) != 1 || text != "" {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		packet = protocol.SceneForcedInput(direction)
	case "action": // action(id, ms)
		action, ok1 := arg(0, 0, 450)
		ms, ok2 := arg(1, 0, 60000)
		if !ok1 || !ok2 || len(n) != 2 || text != "" {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		packet = protocol.SceneForcedAction(action, ms)
	case "effect": // effect(path, ms, x, y)
		ms, ok1 := arg(0, 0, 60000)
		x, ok2 := arg(1, -2000, 2000)
		y, ok3 := arg(2, -2000, 2000)
		if !ok1 || !ok2 || !ok3 || len(n) != 3 || !strings.HasPrefix(text, "Effect/") {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		packet = protocol.SceneEffectAt(text, ms, x, y)
	case "screen": // screen(name): a Map.wz/Effect.img animation over the view
		if named {
			packet = protocol.FieldScreenEffect(text)
		}
	case "sound":
		if named {
			packet = protocol.FieldSoundEffect(text)
		}
	case "music":
		if named {
			packet = protocol.FieldMusic(text)
		}
	default:
		return scripting.CommandResult{}, errors.New("unknown scene operation " + command.Kind)
	}
	if packet == nil {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	return scripting.CommandResult{}, a.sink.WritePacket(packet)
}
