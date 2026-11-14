package world

import (
	"fmt"
	"strings"

	"maple-go-server/internal/protocol"
)

// The demo panel proves the launcher's panel window end to end: @面板 opens
// it, each button press comes back as RecvPanel and the server answers by
// replacing the content. It keeps no state and changes nothing in the game.
const (
	demoPanelWindow uint16 = 1

	demoPanelHello uint16 = 1
	demoPanelClose uint16 = 2
)

func demoPanel(text string) []byte {
	return protocol.PanelPacket(protocol.Panel{
		Window: demoPanelWindow,
		Title:  "自訂視窗",
		Text:   text,
		Buttons: []protocol.PanelButton{
			{ID: demoPanelHello, Label: "打招呼"},
			{ID: demoPanelClose, Label: "關閉"},
		},
	})
}

// handlePanelChat opens the demo panel for @面板 / @panel.
func handlePanelChat(sink packetSink, text string) (bool, error) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) != 1 {
		return false, nil
	}
	switch strings.ToLower(fields[0]) {
	case "@面板", "@panel":
		return true, sink.WritePacket(demoPanel("這個視窗的內容由伺服器決定。\n按下面的按鈕，伺服器會回一行字。"))
	}
	return false, nil
}

// handlePanelClick answers a button of a panel this server opened. A click
// for a window or button it does not know is ignored: the launcher may still
// show a panel from before a reconnect.
func (s *Server) handlePanelClick(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	click, err := protocol.ReadPanelClick(r)
	if err != nil {
		return err
	}
	if click.Window != demoPanelWindow {
		return nil
	}
	switch click.Button {
	case demoPanelHello:
		live.mu.Lock()
		name := live.session.Character.Name
		live.panelClicks++
		count := live.panelClicks
		live.mu.Unlock()
		return sink.WritePacket(demoPanel(fmt.Sprintf("%s 你好，伺服器收到了你的按鈕。\n這是第 %d 次。", name, count)))
	case demoPanelClose:
		return sink.WritePacket(protocol.PanelClosePacket(demoPanelWindow))
	}
	return nil
}
