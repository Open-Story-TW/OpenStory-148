// NPC prompt layouts follow Legacy CField.NPCTalkPacket and NPCTalkType.
package protocol

import (
	"fmt"
	"strings"
)

type NPCMenuChoice struct {
	ID   int64
	Text string
}

type NPCScript struct {
	TemplateID int
	Name       string
}

// Native CNpcPool::OnPacket(0x1b8) registers chat scripts before NPC creation.
// CNpcTemplate::SetScript selects entries by inclusive local YYYYMMDD dates.
func NPCScriptable(entries []NPCScript) []byte {
	if len(entries) == 0 || len(entries) > 255 {
		return nil
	}
	w := NewWriter(0x1db)
	w.U8(byte(len(entries)))
	for _, entry := range entries {
		if entry.TemplateID <= 0 || entry.TemplateID > 9999999 || entry.Name == "" || len(entry.Name) > 128 {
			return nil
		}
		w.I32(int32(entry.TemplateID))
		w.String(entry.Name)
		w.I32(20000101)
		w.I32(20991231)
	}
	return w.Packet()
}

func NPCPrompt(npcID int, kind, text string, choices []NPCMenuChoice) []byte {
	return NPCPromptButtons(npcID, kind, text, choices, false, true)
}

// NPCTalkAvatar is Legacy NPCTalkType ASK_AVATAR (cm.askAvatar →
// getNPCTalkStyle): text, a U8 count and one I32 hair/face ID per entry. The
// client previews each style on the character and answers with its index.
const NPCTalkAvatar = 9

// NPCTalkMapSelection is Legacy cm.askMapSelection (getMapSelection, message
// type 0x10). v148 sub_699B63 reads an I32 window style (1 the old
// CSlideMenuDlg, 0 and 2-4 CSlideMenuDlgEX), an I32 the dialog keeps
// (Legacy sends 1 for the Dimensional Mirror 9010022), then one string of
// "#id#label" entries. OK answers 0x10, 1, I32 id; closing answers 0x10, 0.
const NPCTalkMapSelection = 0x10

// mapSelectionBody is the Legacy "#id#label#id#label" list.
func mapSelectionBody(choices []NPCMenuChoice) (string, bool) {
	if len(choices) == 0 || len(choices) > 64 {
		return "", false
	}
	var body strings.Builder
	seen := map[int64]bool{}
	for _, choice := range choices {
		if choice.ID < 0 || choice.ID > 2147483647 || seen[choice.ID] || choice.Text == "" || len(choice.Text) > 200 || strings.ContainsRune(choice.Text, '#') {
			return "", false
		}
		seen[choice.ID] = true
		fmt.Fprintf(&body, "#%d#%s", choice.ID, choice.Text)
	}
	return body.String(), true
}

func NPCPromptButtons(npcID int, kind, text string, choices []NPCMenuChoice, back, next bool) []byte {
	return NPCPromptSpeaker(npcID, kind, text, choices, back, next, 0)
}

// NPCPromptSpeaker writes the Legacy talk flag byte: 1 hides the close button
// and 2 draws the player as the speaker. Flag 4 would add a second NPC id
// and is not produced.
func NPCPromptSpeaker(npcID int, kind, text string, choices []NPCMenuChoice, back, next bool, speaker int) []byte {
	if npcID <= 0 || len(text) > 16000 || speaker < 0 || speaker > 3 {
		return nil
	}
	if kind == "map_selection" {
		body, ok := mapSelectionBody(choices)
		if !ok {
			return nil
		}
		w := NewWriter(SendNPCTalk)
		w.U8(4)
		w.I32(int32(npcID))
		w.U8(NPCTalkMapSelection)
		w.U8(0)
		// Legacy getMapSelection: Neo City (2083006) style flag, then the
		// Dimensional Mirror (9010022) flag.
		w.I32(boolInt(npcID == 2083006))
		w.I32(boolInt(npcID == 9010022))
		w.String(body)
		return w.Packet()
	}
	msgType := byte(0)
	switch kind {
	case "say":
	case "yes_no":
		msgType = 2
	case "accept":
		// Legacy NPCTalkType.ACCEPT_DECLINE; same empty tail as YES_NO.
		msgType = 0x0E
	case "menu":
		msgType = 5
	case "text":
		msgType = 3
	case "avatar":
		msgType = NPCTalkAvatar
		if len(choices) == 0 || len(choices) > 255 {
			return nil
		}
		for _, choice := range choices {
			if choice.ID < 20000 || choice.ID > 49999 {
				return nil
			}
		}
	default:
		return nil
	}
	if kind == "menu" {
		if len(choices) == 0 || len(choices) > 64 {
			return nil
		}
		var body strings.Builder
		body.WriteString(text)
		seen := map[int64]bool{}
		for _, choice := range choices {
			if choice.ID < 0 || choice.ID > 2147483647 || seen[choice.ID] || len(choice.Text) > 1000 {
				return nil
			}
			seen[choice.ID] = true
			fmt.Fprintf(&body, "\r\n#L%d#%s#l", choice.ID, choice.Text)
		}
		text = body.String()
	}
	w := NewWriter(SendNPCTalk)
	w.U8(4)
	w.I32(int32(npcID))
	w.U8(msgType)
	w.U8(byte(speaker))
	w.String(text)
	if kind == "say" {
		w.Bool(back)
		w.Bool(next)
	}
	if kind == "text" {
		// Legacy getNPCTalkText: empty default text and no length bounds.
		w.I32(0)
		w.I32(0)
	}
	if kind == "avatar" {
		w.U8(byte(len(choices)))
		for _, choice := range choices {
			w.I32(int32(choice.ID))
		}
	}
	return w.Packet()
}

type NPCResponse struct {
	Kind   string
	Choice int64
	Bool   bool
	Back   bool
	Cancel bool
	Text   string
}

func ReadNPCResponse(r *Reader) (NPCResponse, error) {
	msgType, mode := r.U8(), r.U8()
	v := NPCResponse{}
	switch msgType {
	case 0:
		v.Kind = "say"
	case 2:
		v.Kind = "yes_no"
	case 0x0E:
		v.Kind = "accept"
	case 5:
		v.Kind = "menu"
	case NPCTalkMapSelection:
		// v148 sub_699B63: OK sends mode 1 and the chosen entry's id as an
		// I32; closing the window sends mode 0 alone.
		v.Kind = "map_selection"
		switch mode {
		case 1:
			v.Choice = int64(r.I32())
			if r.Err() != nil || r.Remaining() != 0 || v.Choice < 0 {
				return v, ErrMalformed
			}
		case 0, 255:
			v.Cancel = true
			r.Skip(r.Remaining())
		default:
			return v, ErrMalformed
		}
		return v, nil
	case NPCTalkAvatar:
		// Legacy NPCMoreTalk: OK is mode 1 and the chosen index, which the
		// handler reads as an int when four bytes remain and a byte otherwise.
		v.Kind = "avatar"
		switch mode {
		case 1:
			switch r.Remaining() {
			case 1:
				v.Choice = int64(r.U8())
			case 4:
				v.Choice = int64(r.I32())
			default:
				return v, ErrMalformed
			}
			if r.Err() != nil || v.Choice < 0 || v.Choice > 254 {
				return v, ErrMalformed
			}
		case 0, 255:
			v.Cancel = true
			r.Skip(r.Remaining())
		default:
			return v, ErrMalformed
		}
		return v, nil
	case 3:
		v.Kind = "text"
		if mode == 1 {
			v.Text = r.String()
			if r.Err() != nil || r.Remaining() != 0 || len(v.Text) > 64 {
				return v, ErrMalformed
			}
			return v, nil
		}
		if mode != 0 && mode != 255 {
			return v, ErrMalformed
		}
		v.Cancel = true
		return v, nil
	default:
		return v, ErrMalformed
	}
	if mode == 255 {
		v.Cancel = true
	} else if mode > 1 {
		return v, ErrMalformed
	} else {
		v.Bool = mode == 1
		if msgType == 0 && mode == 0 {
			// Native 0x657FDC maps SetRet8193 to1, SetRet8192 to0,
			// and every other return (including Cancel2) to255. A back
			// result is distinct from cancellation even when both are false.
			v.Back = true
		}
		if msgType == 5 {
			if mode != 1 {
				v.Cancel = true
			} else {
				v.Choice = int64(r.I32())
				if v.Choice < 0 {
					return v, ErrMalformed
				}
			}
		}
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return NPCResponse{}, ErrMalformed
	}
	return v, nil
}
