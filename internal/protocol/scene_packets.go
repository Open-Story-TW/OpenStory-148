package protocol

import "strings"

// Cutscene packets of the v148 client. Opcodes and layouts were read from the
// client's own handlers, named in each comment.

// SceneStandAlone hides the other players of the map (0x8ECD64).
func SceneStandAlone(on bool) []byte {
	w := NewWriter(0x167)
	w.Bool(on)
	return w.Packet()
}

// The in-game direction event (0x5535C8) is only read while the direction
// mode of SceneUILock is on.

// SceneForcedAction plays one character action for a time (event 0).
func SceneForcedAction(action, milliseconds int) []byte {
	w := NewWriter(0x17C)
	w.U8(0)
	w.I32(int32(action))
	w.I32(int32(milliseconds))
	return w.Packet()
}

// SceneDelay asks the client to report back after a time (event 1). The
// report is an NPC answer of type NPCTalkSceneDelay.
func SceneDelay(milliseconds int) []byte {
	w := NewWriter(0x17C)
	w.U8(1)
	w.I32(int32(milliseconds))
	return w.Packet()
}

// SceneEffectAt plays an Effect.wz animation at an offset from the character
// (event 2).
func SceneEffectAt(path string, milliseconds, x, y int) []byte {
	if !sceneResource(path) {
		return nil
	}
	w := NewWriter(0x17C)
	w.U8(2)
	w.String(path)
	w.I32(int32(milliseconds))
	w.I32(int32(x))
	w.I32(int32(y))
	w.U8(0) // no layer
	w.U8(0) // not bound to an NPC
	return w.Packet()
}

// SceneForcedInput walks the character: 0 stops, 1 is left, 2 is right
// (event 3).
func SceneForcedInput(direction int) []byte {
	w := NewWriter(0x17C)
	w.U8(3)
	w.I32(int32(direction))
	return w.Packet()
}

// Field effects (0x5121D5): 3 draws a Map.wz/Effect.img animation over the
// screen, 4 plays a Sound.wz effect, 6 changes the background music.
func fieldEffect(kind byte, name string) []byte {
	if !sceneResource(name) {
		return nil
	}
	w := NewWriter(0xDF)
	w.U8(kind)
	w.String(name)
	return w.Packet()
}
func FieldScreenEffect(name string) []byte { return fieldEffect(3, name) }
func FieldSoundEffect(name string) []byte  { return fieldEffect(4, name) }
func FieldMusic(name string) []byte        { return fieldEffect(6, name) }

func sceneResource(name string) bool {
	return name != "" && len(name) <= 160 && !strings.Contains(name, "..") && !strings.ContainsAny(name, "\\\x00")
}

// NPC answer types of the scene prompts (client 0x69B1AB and 0x55320D).
const (
	NPCTalkImages     = 1
	NPCTalkSceneDelay = 0x11
)

// SceneSay is a say window whose speaker is the player (speakerNPC 0) or
// another NPC template (0x699DAF: flag 2 draws the player, flag 4 carries a
// replacement template id; flag 1 removes the close button).
func SceneSay(npcID, speakerNPC int, text string, back, next bool) []byte {
	if npcID <= 0 || len(text) > 16000 || speakerNPC < 0 {
		return nil
	}
	w := NewWriter(SendNPCTalk)
	w.U8(4)
	w.I32(int32(npcID))
	w.U8(0)
	if speakerNPC == 0 {
		w.U8(1 | 2)
	} else {
		w.U8(1 | 4)
		w.I32(int32(speakerNPC))
	}
	w.String(text)
	w.Bool(back)
	w.Bool(next)
	return w.Packet()
}

// SceneImages shows picture pages in one window (0x699F1E).
func SceneImages(npcID int, paths []string) []byte {
	if npcID <= 0 || len(paths) == 0 || len(paths) > 16 {
		return nil
	}
	w := NewWriter(SendNPCTalk)
	w.U8(4)
	w.I32(int32(npcID))
	w.U8(NPCTalkImages)
	w.U8(1)
	w.U8(byte(len(paths)))
	for _, p := range paths {
		if !sceneResource(p) {
			return nil
		}
		w.String(p)
	}
	return w.Packet()
}
