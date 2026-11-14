package world

import (
	"context"
	"errors"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type buddyPeer struct {
	live    *liveCharacter
	sink    packetSink
	list    domain.BuddyList
	channel int
	budget  chatBudget
}

type buddySpace struct {
	// Only buddy transitions serialize here. Movement, combat and other
	// characters' script runtimes never wait on a buddy database operation.
	mu    sync.Mutex
	peers map[int64]*buddyPeer
}

func newBuddySpace() *buddySpace { return &buddySpace{peers: map[int64]*buddyPeer{}} }

func acceptedBuddy(list domain.BuddyList, id int64) bool {
	for _, e := range list.Entries {
		if e.CharacterID == id {
			return !e.Pending
		}
	}
	return false
}

func (b *buddySpace) visibleList(id int64, list domain.BuddyList) domain.BuddyList {
	copy := domain.BuddyList{Capacity: list.Capacity, Entries: append([]domain.BuddyEntry(nil), list.Entries...)}
	for i := range copy.Entries {
		e := &copy.Entries[i]
		e.Channel = 0
		if peer := b.peers[e.CharacterID]; !e.Pending && peer != nil && acceptedBuddy(peer.list, id) {
			e.Channel = peer.channel
		}
	}
	return copy
}

func (b *buddySpace) notifyPresence(id int64) {
	current := b.peers[id]
	for otherID, peer := range b.peers {
		if otherID == id || !acceptedBuddy(peer.list, id) {
			continue
		}
		channel := 0
		if current != nil && acceptedBuddy(current.list, otherID) {
			channel = current.channel
		}
		_ = peer.sink.WritePacket(protocol.BuddyStatus(id, channel))
	}
}

func (s *Server) enterBuddies(ctx context.Context, live *liveCharacter, sink packetSink) error {
	repo, ok := s.repo.(domain.BuddyRepository)
	if !ok || s.buddies == nil {
		return nil
	}
	b := s.buddies
	b.mu.Lock()
	defer b.mu.Unlock()
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	live.mu.Unlock()
	readCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	list, err := repo.ReadBuddies(readCtx, session)
	if err != nil {
		return err
	}
	b.peers[session.CharacterID] = &buddyPeer{live: live, sink: sink, list: list, channel: session.ChannelID}
	if err = sink.WritePacket(protocol.BuddyCapacity(domain.EffectiveBuddyCapacity(list.Capacity))); err != nil {
		return err
	}
	initial := b.visibleList(session.CharacterID, list)
	entries := initial.Entries
	initial.Entries = initial.Entries[:0]
	for _, entry := range entries {
		if !entry.Pending {
			initial.Entries = append(initial.Entries, entry)
		}
	}
	// Native action9 appends its pending record. Restoring that record in the
	// initial action7 list too creates two entries for the same character.
	if err = sink.WritePacket(protocol.BuddyListPacket(initial, 7)); err != nil {
		return err
	}
	for _, e := range list.Entries {
		if e.Pending {
			channel := 0
			if peer := b.peers[e.CharacterID]; peer != nil {
				channel = peer.channel
			}
			if err = sink.WritePacket(protocol.BuddyInvitation(e.CharacterID, e.Name, e.Level, e.Job, e.SubJob, channel)); err != nil {
				return err
			}
		}
	}
	b.notifyPresence(session.CharacterID)
	return nil
}

func (s *Server) leaveBuddies(live *liveCharacter) {
	if s.buddies == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	b := s.buddies
	b.mu.Lock()
	defer b.mu.Unlock()
	if peer := b.peers[id]; peer == nil || peer.live != live {
		return
	}
	delete(b.peers, id)
	b.notifyPresence(id)
}

func (s *Server) handleBuddy(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	q, err := protocol.ReadBuddyRequest(r)
	if err != nil {
		return err
	}
	repo, ok := s.repo.(domain.BuddyRepository)
	if !ok || s.buddies == nil {
		return sink.WritePacket(protocol.BuddyFailure(15))
	}
	b := s.buddies
	b.mu.Lock()
	defer b.mu.Unlock()
	live.mu.Lock()
	q.Session = live.session
	q.Session.Lease = s.currentLease()
	live.mu.Unlock()
	peer := b.peers[q.Session.CharacterID]
	if peer == nil || peer.live != live {
		return domain.ErrStaleOwner
	}
	if q.Action == protocol.BuddyReload {
		return sink.WritePacket(protocol.BuddyListPacket(b.visibleList(q.Session.CharacterID, peer.list), 7))
	}
	if !peer.budget.take(time.Now()) {
		return sink.WritePacket(protocol.BuddyFailure(13))
	}
	writeCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	result, err := repo.MutateBuddy(writeCtx, q)
	if err != nil {
		code := uint8(15)
		switch {
		case errors.Is(err, domain.ErrBuddyFull):
			code = 11
		case errors.Is(err, domain.ErrBuddyTargetFull):
			code = 12
		case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrForbidden):
			code = 13
		case errors.Is(err, domain.ErrStaleOwner):
			return err
		case errors.Is(err, domain.ErrNotFound):
			code = 15
		default:
			return err
		}
		return sink.WritePacket(protocol.BuddyFailure(code))
	}
	peer.list = result.List
	other := b.peers[result.TargetID]
	if other != nil {
		other.list = result.TargetList
	}
	action := uint8(10)
	if q.Action == 3 {
		action = 18
	}
	if err = sink.WritePacket(protocol.BuddyListPacket(b.visibleList(q.Session.CharacterID, peer.list), action)); err != nil {
		return err
	}
	if other != nil {
		if result.Invitation {
			// OnFriendResult action9 itself appends the pending friend. A prior
			// full snapshot containing that same pending entry duplicates it.
			_ = other.sink.WritePacket(protocol.BuddyInvitation(q.Session.CharacterID, result.SenderName, result.SenderLevel, result.SenderJob, result.SenderSubJob, peer.channel))
		} else if err = other.sink.WritePacket(protocol.BuddyListPacket(b.visibleList(result.TargetID, other.list), 10)); err != nil {
			// The durable mutation remains valid if a recipient closes its socket.
			// Its next login restores the list and outstanding invitation.
			return nil
		}
	}
	return nil
}
