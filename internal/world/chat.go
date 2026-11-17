package world

import (
	"context"
	"strings"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// A small, per-connection bucket also limits modified clients which bypass the
// native CChatHelper rate check. It never performs database work per message.
type chatBudget struct {
	tokens int
	last   time.Time
	// ui is the launcher interface mask this chat path last sent, plus one so
	// that zero means none yet.
	ui uint64
}

// allow spends one line of the budget unless the operator has lifted the
// chat flood limits (domain.ChatUnlimited).
func (b *chatBudget) allow(now time.Time) bool {
	return domain.ChatUnlimited() || b.take(now)
}

// staleUI reports whether the player's launcher should be sent the current
// interface mask. Players otherwise get a changed mask on their next map; a
// chat line carries it sooner, so lifting or restoring the chat limits in the
// admin reaches someone who is chatting at once.
func (b *chatBudget) staleUI() bool {
	mask := uint64(protocol.ClientUI()) + 1
	if b.ui == mask {
		return false
	}
	b.ui = mask
	return true
}

func (b *chatBudget) take(now time.Time) bool {
	const interval = 500 * time.Millisecond
	if b.last.IsZero() {
		b.last, b.tokens = now, 4
	}
	if elapsed := now.Sub(b.last); elapsed >= interval {
		refill := int(elapsed / interval)
		b.tokens = min(4, b.tokens+refill)
		b.last = b.last.Add(time.Duration(refill) * interval)
	}
	if b.tokens == 0 {
		return false
	}
	b.tokens--
	return true
}

func (s *Server) handleGeneralChat(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadGeneralChat(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil
	}
	live.mu.Lock()
	session := live.session
	allowed := live.chat.allow(time.Now())
	staleUI := live.chat.staleUI()
	live.mu.Unlock()
	if staleUI {
		if err := sink.WritePacket(protocol.ClientUIPacket()); err != nil {
			return err
		}
	}
	if !allowed {
		return nil
	}
	if handled, err := handlePreferenceChat(sink, req.Text); handled || err != nil {
		return err
	}
	if handled, err := handlePanelChat(sink, req.Text); handled || err != nil {
		return err
	}
	if handled, err := s.handleGMChat(ctx, live, sink, req.Text); handled || err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(req.Text), "@event") {
		return s.joinGMEvent(ctx, session.ChannelID, live, sink)
	}
	if session.Character.MapID == oxQuizMap && s.oxQuizInProgress(session.ChannelID) {
		return nil
	}
	if s.maps == nil {
		return nil
	}
	v := s.sceneFor(live, sink)
	if v == nil {
		return nil
	}
	unlock, err := s.lockScenePublication(session.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(v) {
		return nil
	}
	recordChat("chat", session, req.Text)
	packet := protocol.GeneralChat(session.CharacterID, req.Text, req.Show)
	s.maps.broadcastSpeech(session.ChannelID, session.Character.MapID, session.CharacterID, packet)
	return nil
}

func (m *mapSpace) broadcastSpeech(channel, mapID int, characterID int64, packet []byte) {
	if len(packet) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.rooms[mapKey{channel: channel, mapID: mapID}]
	self := room[characterID]
	if self == nil {
		return
	}
	_ = self.sink.WritePacket(packet)
	for id := range self.visible {
		if peer := room[id]; peer != nil {
			_ = peer.sink.WritePacket(packet)
		}
	}
}
