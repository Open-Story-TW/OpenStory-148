package world

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// npcGMNotice writes one "[GM NPC]" chat line to a GM character: which NPC
// was clicked, the script and line a conversation reached, each answer and
// host command, and why the server did not handle it. Like [GM 任務], it is
// read-only text for the clicking GM; ordinary players never receive it.
func (s *Server) npcGMNotice(live *liveCharacter, sink packetSink, npcID int, format string, args ...any) {
	if live == nil || sink == nil {
		return
	}
	live.mu.Lock()
	gm := live.session.Character.GM
	live.mu.Unlock()
	if gm == 0 {
		return
	}
	prefix := fmt.Sprintf("[GM NPC] %d", npcID)
	if name, ok := content.NPCName(os.Getenv("MAPLEGO_WZ_ROOT"), npcID); ok {
		prefix += "「" + name + "」"
	}
	_ = sink.WritePacket(protocol.PortalTrainingMessage(prefix + " " + fmt.Sprintf(format, args...)))
}

func (a *worldScript) gmNotice(format string, args ...any) {
	if a.server == nil {
		return
	}
	switch a.kind {
	case "npc":
		a.server.npcGMNotice(a.live, a.sink, a.npcID, format, args...)
	case "portal":
		a.server.portalGMNotice(a.live, a.sink, a.mapID, a.portal, format, args...)
	}
}

// portalGMNotice writes one "[GM Portal]" chat line to a GM character: the
// map and portal stepped on, the script it names, whether the server has that
// script (and, for a Legacy script not migrated yet, why), each non-query
// command with its outcome, and how the invocation ended. Ordinary players
// never receive it.
func (s *Server) portalGMNotice(live *liveCharacter, sink packetSink, mapID int, p content.Portal, format string, args ...any) {
	if live == nil || sink == nil {
		return
	}
	live.mu.Lock()
	gm := live.session.Character.GM
	live.mu.Unlock()
	if gm == 0 {
		return
	}
	prefix := fmt.Sprintf("[GM Portal] %d/%s「%s」", mapID, p.Name, p.Script)
	_ = sink.WritePacket(protocol.PortalTrainingMessage(prefix + " " + fmt.Sprintf(format, args...)))
}

var npcPromptNames = map[string]string{"say": "對話", "yes_no": "是/否", "accept": "接受/拒絕", "menu": "選單", "text": "輸入文字", "avatar": "造型預覽", "map_selection": "地圖選單"}

// gmShort is a one-line excerpt of dialogue text with colour/format codes
// left in but line breaks folded, bounded so a notice stays one chat line.
func gmShort(text string, runes int) string {
	text = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(text)
	if utf8.RuneCountInString(text) <= runes {
		return text
	}
	return string([]rune(text)[:runes]) + "…"
}

func (a *worldScript) gmEvent(event scripting.Event) {
	switch event.Kind {
	case "prompt":
		p := event.Prompt
		if p == nil {
			return
		}
		detail := ""
		switch p.Kind {
		case "menu", "map_selection":
			ids := make([]string, 0, len(p.Choices))
			for _, c := range p.Choices {
				ids = append(ids, fmt.Sprint(c.ID))
			}
			detail = " 選項:" + strings.Join(ids, ",")
		case "avatar":
			detail = fmt.Sprintf(" 樣式:%d種", len(p.Choices))
		}
		quoted := ""
		if p.Text != "" {
			quoted = "「" + gmShort(p.Text, 24) + "」"
		}
		a.gmNotice("%s 第%d行 顯示:%s%s%s", event.Script, event.Line, npcPromptNames[p.Kind], detail, quoted)
	case "done":
		a.gmNotice("%s 第%d行 結束", event.Script, event.Line)
	case "paused":
		line := event.Line
		if event.Debug != nil {
			line = event.Debug.Line
		}
		a.gmNotice("%s 第%d行 除錯暫停", event.Script, line)
	}
}

func gmResponse(kind string, r protocol.NPCResponse, p *scripting.Prompt) string {
	switch {
	case r.Cancel:
		return "取消/關閉"
	case kind == "say" && r.Back:
		return "上一頁"
	case kind == "say":
		return "下一頁"
	case kind == "yes_no" || kind == "accept":
		if r.Bool {
			return "是"
		}
		return "否"
	case kind == "menu" || kind == "map_selection":
		return fmt.Sprintf("選項 %d", r.Choice)
	case kind == "avatar":
		if p != nil && r.Choice >= 0 && r.Choice < int64(len(p.Choices)) {
			return fmt.Sprintf("第%d個樣式 %d", r.Choice+1, p.Choices[r.Choice].ID)
		}
		return fmt.Sprintf("第%d個樣式(超出範圍)", r.Choice+1)
	case kind == "text":
		return "輸入「" + gmShort(r.Text, 16) + "」"
	}
	return kind
}

// Queries are frequent and side-effect free; only their failures are shown.
var gmQuietCommands = map[string]bool{"map_id": true, "level": true, "job": true, "hp": true, "mesos": true, "item_count": true, "can_hold": true, "quest_state": true, "quest_data": true, "read_log": true, "gender": true, "hair": true, "face": true, "guild_rank": true, "guild_capacity_limit": true, "alliance_rank": true, "style_filter": true,
	"portal_name": true, "portal_id": true, "player_count": true, "monster_count": true, "profession_level": true, "fame": true, "gm": true, "party_size": true, "party_leader": true}

func (a *worldScript) gmCommand(command scripting.Command, result scripting.CommandResult, err error) {
	if err == nil && gmQuietCommands[command.Kind] {
		return
	}
	args := ""
	if len(command.Ints) > 0 {
		parts := make([]string, 0, min(len(command.Ints), 6))
		for i, v := range command.Ints {
			if i == 5 {
				parts = append(parts, fmt.Sprintf("…共%d個參數", len(command.Ints)))
				break
			}
			parts = append(parts, fmt.Sprint(v))
		}
		if command.Kind == "warp" && command.Text != "" {
			parts = append(parts, `"`+gmShort(command.Text, 16)+`"`) // a named destination portal
		}
		args = "(" + strings.Join(parts, ",") + ")"
	}
	outcome := "完成"
	if err != nil {
		outcome = "錯誤:" + err.Error()
	} else if m, ok := result.Value.(map[string]any); ok {
		if okValue, has := m["ok"].(bool); has && !okValue {
			outcome = fmt.Sprintf("拒絕:%v", m["reason"])
		} else if style, has := m["style"]; has {
			outcome = fmt.Sprintf("完成 樣式=%v", style)
		}
	}
	a.gmNotice("%s 第%d行 指令 %s%s → %s", command.Script, command.Line, command.Kind, args, outcome)
}
