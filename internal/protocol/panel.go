package protocol

import "fmt"

// A panel is a window the launcher builds inside the game from what the
// server describes: a title, a block of text and a row of buttons. The native
// client has no such window; the launcher consumes SendPanel and sends
// RecvPanel itself when a button is pressed, so neither opcode reaches or
// leaves native code. The server owns the contents and every decision; the
// launcher only draws and reports clicks.
// Wire format: Open-Maple docs/PANEL.md.
const (
	SendPanel uint16 = 0x616
	RecvPanel uint16 = 0x616

	panelVersion = 1

	panelClose uint8 = 0
	panelShow  uint8 = 1

	// Launcher limits, in Big5 bytes. A frame over any of them is rejected
	// whole by the launcher, so the encoder refuses it first.
	PanelMaxTitle   = 64
	PanelMaxText    = 1024
	PanelMaxLabel   = 32
	PanelMaxButtons = 8
)

// PanelButton is one button of a panel. ID comes back in PanelClick.
type PanelButton struct {
	ID    uint16
	Label string
}

// Panel is the whole content of one panel window. Showing a panel whose
// Window is already open replaces its content in place.
type Panel struct {
	Window  uint16
	Title   string
	Text    string // Lines are separated by "\n".
	Buttons []PanelButton
}

func panelString(w *Writer, v string, limit int) {
	start := len(w.data)
	w.String(v)
	if w.err == nil && len(w.data)-start-2 > limit {
		w.err = fmt.Errorf("%w: panel string over %d bytes", ErrMalformed, limit)
	}
}

// PanelPacket opens p or replaces the content of the open panel with the same
// Window. It is nil when p breaks a launcher limit or has text Big5 cannot hold.
func PanelPacket(p Panel) []byte {
	if len(p.Buttons) > PanelMaxButtons {
		return nil
	}
	w := NewWriter(SendPanel)
	w.U8(panelVersion)
	w.U8(panelShow)
	w.U16(p.Window)
	panelString(w, p.Title, PanelMaxTitle)
	panelString(w, p.Text, PanelMaxText)
	w.U8(uint8(len(p.Buttons)))
	for _, b := range p.Buttons {
		w.U16(b.ID)
		panelString(w, b.Label, PanelMaxLabel)
	}
	return w.Packet()
}

// PanelClosePacket closes the panel window; closing one that is not open does
// nothing.
func PanelClosePacket(window uint16) []byte {
	w := NewWriter(SendPanel)
	w.U8(panelVersion)
	w.U8(panelClose)
	w.U16(window)
	return w.Packet()
}

// PanelClick is a button press in a panel the server opened.
type PanelClick struct {
	Window uint16
	Button uint16
}

// ReadPanelClick decodes the five bytes after the opcode.
func ReadPanelClick(r *Reader) (PanelClick, error) {
	version := r.U8()
	click := PanelClick{Window: r.U16(), Button: r.U16()}
	if r.Err() != nil {
		return PanelClick{}, r.Err()
	}
	if version != panelVersion || r.Remaining() != 0 {
		return PanelClick{}, fmt.Errorf("%w: panel click", ErrMalformed)
	}
	return click, nil
}
