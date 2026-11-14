package world

import (
	"fmt"
	"os"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

var questActionNames = map[uint8]string{1: "接受", 2: "完成", 3: "放棄", 4: "接受(腳本)", 5: "完成(腳本)"}
var questStateNames = map[int]string{0: "未開始", 1: "進行中", 2: "已完成"}

func questStateOf(c domain.Character, id int) int {
	for _, q := range c.Quests {
		if q.ID == id {
			return q.State
		}
	}
	return 0
}

// questGMNotice tells a GM character which quest the client just clicked, its
// state before and after, and why the server refused it. It is read-only chat
// text for the clicking GM; ordinary players never receive it.
func (s *Server) questGMNotice(live *liveCharacter, sink packetSink, request protocol.QuestActionRequest, before int, outcome string) {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	if c.GM == 0 {
		return
	}
	name, problems := "", []string(nil)
	definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), request.QuestID)
	if err != nil {
		problems = append(problems, "無任務資料: "+err.Error())
	} else {
		name = "「" + definition.Name + "」"
		for _, part := range [][]string{definition.Unsupported, definition.Start.Unsupported, definition.Complete.Unsupported, definition.OnStart.Unsupported, definition.OnComplete.Unsupported} {
			problems = append(problems, part...)
		}
		for _, script := range []string{definition.Start.Script, definition.Complete.Script} {
			if script != "" {
				problems = append(problems, "WZ腳本="+script)
			}
		}
	}
	text := fmt.Sprintf("[GM 任務] ID %d%s NPC %d 動作:%s 狀態:%s", request.QuestID, name, request.NPCID, questActionNames[request.Action], questStateNames[before])
	if after := questStateOf(c, request.QuestID); after != before {
		text += "→" + questStateNames[after]
	}
	text += " 結果:" + outcome
	_ = sink.WritePacket(protocol.PortalTrainingMessage(text))
	if len(problems) > 0 {
		_ = sink.WritePacket(protocol.PortalTrainingMessage(fmt.Sprintf("[GM 任務] ID %d 未支援項目: %s", request.QuestID, strings.Join(problems, ", "))))
	}
}
