package world

import (
	"strconv"
	"strings"

	"maple-go-server/internal/protocol"
)

// Preference commands let a player change the launcher's own display settings
// from chat instead of editing a file. The launcher applies and remembers
// them; the server stores nothing and the text is never shown to other players.

var preferenceResolutions = []string{"800x600", "1024x768", "1280x720", "1366x768", "1600x900", "1768x992"}

// Render layer indices of the launcher's mask.
var preferenceLayers = map[string]int32{
	"背景": 0, "background": 0, "前景": 1, "foreground": 1, "地磚": 2, "tiles": 2, "物件": 3, "objects": 3,
	"角色": 4, "characters": 4, "怪物": 5, "mobs": 5, "npc": 6, "特效": 7, "effects": 7,
	"傷害": 8, "damage": 8, "掉落": 9, "drops": 9, "畫面": 10, "frames": 10,
}

const preferenceHelp = "顯示設定指令：@fps 60（0 為不限制）、@解析度 1280x720、@視窗 開/關、@顯示 怪物 開/關、@背景輸入 開/關、@重設。" +
	"可用解析度：800x600、1024x768、1280x720、1366x768、1600x900、1768x992。" +
	"可切換的顯示項目：背景、前景、地磚、物件、角色、怪物、NPC、特效、傷害、掉落、畫面。"

func preferenceSwitch(word string) (int32, bool) {
	switch strings.ToLower(word) {
	case "開", "开", "on", "1", "true":
		return 1, true
	case "關", "关", "off", "0", "false":
		return 0, true
	}
	return 0, false
}

// parsePreferenceChat reports whether text is a preference command, and for a
// valid one the launcher packet and the confirmation to show.
func parsePreferenceChat(text string) (handled bool, packet []byte, reply string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "@") {
		return false, nil, ""
	}
	name, args := strings.ToLower(strings.TrimPrefix(fields[0], "@")), fields[1:]
	switch name {
	case "設定", "设定", "set", "settings", "help", "說明":
		return true, nil, preferenceHelp
	case "重設", "reset":
		if len(args) == 0 {
			return true, protocol.ClientPreferencePacket(protocol.ClientPrefReset, 0), "顯示設定已還原成預設值。視窗與解析度在下次開啟遊戲時生效。"
		}
	case "fps":
		if len(args) == 1 {
			if n, err := strconv.Atoi(args[0]); err == nil && n >= 0 && n <= 240 {
				reply = "FPS 上限已設為 " + args[0] + "。"
				if n == 0 {
					reply = "已取消 FPS 上限。"
				}
				return true, protocol.ClientPreferencePacket(protocol.ClientPrefMaxFPS, int32(n)), reply
			}
		}
		return true, nil, "用法：@fps 60（0 到 240，0 為不限制）"
	case "解析度", "res", "resolution":
		if len(args) == 1 {
			want := strings.ToLower(strings.ReplaceAll(args[0], "*", "x"))
			for index, size := range preferenceResolutions {
				if want == size || want == strconv.Itoa(index) {
					return true, protocol.ClientPreferencePacket(protocol.ClientPrefResolution, int32(index)), "解析度已設為 " + size + "，下次開啟遊戲時生效。"
				}
			}
		}
		return true, nil, "用法：@解析度 1280x720。可用：" + strings.Join(preferenceResolutions, "、")
	case "視窗", "视窗", "window", "windowed":
		if len(args) == 1 {
			if value, ok := preferenceSwitch(args[0]); ok {
				reply = "已改為全螢幕，下次開啟遊戲時生效。"
				if value == 1 {
					reply = "已改為視窗模式，下次開啟遊戲時生效。"
				}
				return true, protocol.ClientPreferencePacket(protocol.ClientPrefWindowed, value), reply
			}
		}
		return true, nil, "用法：@視窗 開 或 @視窗 關"
	case "顯示", "显示", "show", "render":
		if len(args) == 2 {
			layer, known := preferenceLayers[strings.ToLower(args[0])]
			if value, ok := preferenceSwitch(args[1]); ok && known {
				return true, protocol.ClientPreferencePacket(protocol.ClientPrefRenderLayer, layer|value<<8), "已" + map[int32]string{0: "隱藏", 1: "顯示"}[value] + args[0] + "。"
			}
		}
		return true, nil, "用法：@顯示 怪物 關。項目：背景、前景、地磚、物件、角色、怪物、NPC、特效、傷害、掉落、畫面"
	case "背景輸入", "背景输入", "bginput":
		if len(args) == 1 {
			if value, ok := preferenceSwitch(args[0]); ok {
				return true, protocol.ClientPreferencePacket(protocol.ClientPrefBackgroundInput, value), "背景輸入設定已儲存，下次開啟遊戲時生效。"
			}
		}
		return true, nil, "用法：@背景輸入 開 或 @背景輸入 關"
	default:
		return false, nil, ""
	}
	return true, nil, preferenceHelp
}

func handlePreferenceChat(sink packetSink, text string) (bool, error) {
	handled, packet, reply := parsePreferenceChat(text)
	if !handled {
		return false, nil
	}
	if packet != nil {
		if err := sink.WritePacket(packet); err != nil {
			return true, err
		}
	}
	return true, sink.WritePacket(protocol.Notice(reply))
}
