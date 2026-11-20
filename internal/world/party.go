package world

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type partyPeer struct {
	live     *liveCharacter
	sink     packetSink
	party    domain.PartySnapshot
	budget   chatBudget
	queue    chan partyBatch
	stop     chan struct{}
	stopOnce sync.Once
	failed   bool // Protected by partySpace.mu.
	hpSeen   map[int64]partyHPState
}
type partySpace struct {
	commands sync.Mutex
	mu       sync.Mutex
	peers    map[int64]*partyPeer
	exp      *expeditions // Protected by mu; changed only under commands.
	gameplay *gameplaySpace
}

func newPartySpace() *partySpace {
	return &partySpace{peers: map[int64]*partyPeer{}, exp: newExpeditions()}
}

type partyDelivery struct {
	peer    *partyPeer
	packets [][]byte
}

var errPartySlowPeer = errors.New("party recipient queue unavailable")

type partyBatch struct {
	packets [][]byte
	done    chan error
}
type partyWait struct {
	peer *partyPeer
	done <-chan error
}

func newPartyPeer(live *liveCharacter, sink packetSink, p domain.PartySnapshot) *partyPeer {
	peer := &partyPeer{live: live, sink: sink, party: p, queue: make(chan partyBatch, 32), stop: make(chan struct{})}
	go peer.writeLoop()
	return peer
}
func (p *partyPeer) halt() { p.stopOnce.Do(func() { close(p.stop) }) }
func (p *partyPeer) closeSink() {
	if c, ok := p.sink.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}
func (p *partyPeer) writeLoop() {
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		select {
		case <-p.stop:
			return
		case batch := <-p.queue:
			var err error
			for _, packet := range batch.packets {
				if packet != nil {
					if err = p.sink.WritePacket(packet); err != nil {
						break
					}
				}
			}
			batch.done <- err
			if err != nil {
				p.halt()
				p.closeSink()
				return
			}
		}
	}
}

// Enqueue under partySpace.mu, which determines the order for every recipient.
// The bounded peer writer owns all party writes; no global lock is held while
// it waits on a socket. Saturation closes that recipient instead of dropping
// essential created/invite/departure frames or publishing an older snapshot.
func queueParty(out []partyDelivery) []partyWait {
	waits := make([]partyWait, 0, len(out))
	for _, d := range out {
		done := make(chan error, 1)
		if d.peer.failed {
			done <- errPartySlowPeer
		} else {
			select {
			case <-d.peer.stop:
				done <- errPartySlowPeer
			default:
				select {
				case d.peer.queue <- partyBatch{packets: d.packets, done: done}:
				default:
					d.peer.failed = true
					d.peer.halt()
					done <- errPartySlowPeer
					go d.peer.closeSink()
				}
			}
		}
		waits = append(waits, partyWait{peer: d.peer, done: done})
	}
	return waits
}

func partySession(live *liveCharacter) domain.CharacterSession {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.session
}

func (b *partySpace) visible(p domain.PartySnapshot) domain.PartySnapshot {
	p.Members = append([]domain.PartyMember(nil), p.Members...)
	for i := range p.Members {
		m := &p.Members[i]
		m.Channel, m.MapID, m.HP, m.MaxHP = 0, 0, 0, 0
		m.TownPortal = domain.PartyTownPortal{}
		peer := b.peers[m.CharacterID]
		if peer == nil || peer.party.ID != p.ID {
			continue
		}
		s := partySession(peer.live)
		if s.CharacterID != m.CharacterID || s.Lease.WorldID != p.WorldID {
			continue
		}
		m.Name, m.Level, m.Job, m.SubJob = s.Character.Name, s.Character.Level, s.Character.Job, s.Character.SubJob
		m.Channel, m.MapID, m.HP, m.MaxHP = s.ChannelID, s.Character.MapID, s.Character.HP, s.Character.MaxHP
		m.TownPortal = b.gameplay.partyTownPortal(s, p.ID, m.Slot)
	}
	return p
}

func partyPortalsFor(p domain.PartySnapshot, receiverID int64) domain.PartySnapshot {
	receiver, _ := p.Member(receiverID)
	p.Members = append([]domain.PartyMember(nil), p.Members...)
	for i := range p.Members {
		m := &p.Members[i]
		if receiver.Channel == 0 || m.Channel != receiver.Channel {
			m.TownPortal = domain.PartyTownPortal{}
		}
	}
	return p
}

func (b *partySpace) snapshots(p domain.PartySnapshot) []partyDelivery {
	if p.ID == 0 {
		return nil
	}
	p = b.visible(p)
	var out []partyDelivery
	for _, m := range p.Members {
		if peer := b.peers[m.CharacterID]; peer != nil && peer.party.ID == p.ID {
			peer.party = p
			packet := protocol.PartySnapshotPacket(partyPortalsFor(p, m.CharacterID))
			out = append(out, partyDelivery{peer: peer, packets: [][]byte{packet}})
		}
	}
	return out
}

func (s *Server) enterParty(ctx context.Context, live *liveCharacter, sink packetSink) error {
	repo, ok := s.repo.(domain.PartyRepository)
	if !ok || s.parties == nil {
		return nil
	}
	b := s.parties
	b.commands.Lock()
	locked := true
	unlock := func() {
		if locked {
			b.commands.Unlock()
			locked = false
		}
	}
	defer unlock()
	q := partySession(live)
	q.Lease = s.currentLease()
	readCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	state, err := repo.ReadParty(readCtx, q)
	if err != nil {
		return err
	}
	b.mu.Lock()
	for _, other := range b.peers {
		delete(other.hpSeen, q.CharacterID)
	}
	if old := b.peers[q.CharacterID]; old != nil {
		old.halt()
	}
	peer := newPartyPeer(live, sink, state.Party)
	b.peers[q.CharacterID] = peer
	// Every online member's cache is the same durable revision. No detached
	// checkpoint's current HP, channel or map is used for a peer snapshot.
	for _, m := range state.Party.Members {
		if other := b.peers[m.CharacterID]; other != nil {
			other.party = state.Party
		}
	}
	out := b.snapshots(state.Party)
	out = append(out, s.partyHPDeltas(b.visible(state.Party))...)
	out = append(out, b.expeditionEnter(peer)...)
	out = append(out, b.expeditionPresence(state.Party)...)
	for _, i := range state.Invitations {
		if from := b.peers[i.InviterID]; from != nil {
			c := partySession(from.live).Character
			i.InviterName, i.InviterLevel, i.InviterJob, i.InviterSubJob = c.Name, c.Level, c.Job, c.SubJob
		}
		out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.PartyInvitationPacket(i)}})
	}
	waits := queueParty(out)
	b.mu.Unlock()
	unlock()
	var ownError error
	for _, w := range waits {
		if w.peer == peer {
			select {
			case err := <-w.done:
				if err != nil {
					ownError = err
				}
			case <-readCtx.Done():
				return readCtx.Err()
			}
		}
	}
	return ownError
}

// Disconnect/transfer only changes presence. Leader and offline membership
// remain durable until a member explicitly leaves or the leader disbands.
func (s *Server) leaveParty(live *liveCharacter) {
	if s.parties == nil {
		return
	}
	id := partySession(live).CharacterID
	b := s.parties
	b.mu.Lock()
	peer := b.peers[id]
	if peer == nil || peer.live != live {
		b.mu.Unlock()
		return
	}
	delete(b.peers, id)
	for _, other := range b.peers {
		delete(other.hpSeen, id)
	}
	peer.halt()
	out := b.snapshots(peer.party)
	out = append(out, b.expeditionPresence(peer.party)...)
	queueParty(out)
	b.mu.Unlock()
}

func samePartyRuntime(a, b domain.PartySnapshot) bool {
	if a.ID != b.ID || a.Revision != b.Revision || len(a.Members) != len(b.Members) {
		return false
	}
	for i, m := range a.Members {
		n := b.Members[i]
		if m.CharacterID != n.CharacterID || m.Channel != n.Channel || m.MapID != n.MapID || m.Level != n.Level || m.Job != n.Job {
			return false
		}
	}
	return true
}

// Map/job/level/channel changes need a party snapshot; XY movement does not.
// HP fields are copied from live characters by visible(), ready for the native
// HP observer/packet follow-up, and are never refreshed from SQL here.
func (s *Server) refreshParty(live *liveCharacter) {
	if s.parties == nil {
		return
	}
	id := partySession(live).CharacterID
	b := s.parties
	b.mu.Lock()
	peer := b.peers[id]
	if peer == nil || peer.live != live || peer.party.ID == 0 {
		b.mu.Unlock()
		return
	}
	p := b.visible(peer.party)
	var out []partyDelivery
	if !samePartyRuntime(peer.party, p) {
		for _, m := range p.Members {
			if other := b.peers[m.CharacterID]; other != nil && other.party.ID == p.ID {
				other.party = p
			}
		}
		out = b.snapshots(p)
		out = append(out, b.expeditionPresence(p)...)
	}
	out = append(out, s.partyHPDeltas(p)...)
	queueParty(out)
	b.mu.Unlock()
}

var partyMonitorLabels = map[domain.PartyAction]string{
	domain.PartyCreate: "建立隊伍", domain.PartyLeave: "離開隊伍", domain.PartyInvite: "邀請", domain.PartyExpel: "踢出",
	domain.PartyLeader: "轉讓隊長給", domain.PartyAccept: "加入隊伍", domain.PartyDeny: "拒絕邀請",
}

func (s *Server) handleParty(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	q, err := protocol.ReadPartyRequest(r)
	if err != nil {
		return err
	}
	return s.applyParty(ctx, live, sink, q)
}
func (s *Server) handlePartyReply(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	q, err := protocol.ReadPartyReply(r)
	if err != nil {
		return err
	}
	return s.applyParty(ctx, live, sink, q)
}

func (s *Server) applyParty(ctx context.Context, live *liveCharacter, sink packetSink, q domain.PartyRequest) error {
	repo, ok := s.repo.(domain.PartyRepository)
	if !ok || s.parties == nil {
		slog.Info("party_rejected", "action", q.Action, "reason", "unavailable")
		return sink.WritePacket(protocol.PartyFailure(21))
	}
	q.Session = partySession(live)
	q.Session.Lease = s.currentLease()
	b := s.parties
	b.commands.Lock()
	locked := true
	unlock := func() {
		if locked {
			b.commands.Unlock()
			locked = false
		}
	}
	defer unlock()
	b.mu.Lock()
	peer := b.peers[q.Session.CharacterID]
	if peer == nil || peer.live != live {
		b.mu.Unlock()
		return domain.ErrStaleOwner
	}
	if !peer.budget.take(time.Now()) {
		b.mu.Unlock()
		unlock()
		slog.Info("party_rejected", "character", q.Session.CharacterID, "action", q.Action, "reason", "rate_limited")
		return sink.WritePacket(protocol.PartyFailure(21))
	}
	if q.Action == domain.PartyInvite {
		for id, p := range b.peers {
			target := partySession(p.live)
			if target.Lease.WorldID == q.Session.Lease.WorldID && strings.EqualFold(target.Character.Name, q.Name) {
				q.TargetID = id
				q.Name = target.Character.Name
				break
			}
		}
		if q.TargetID == 0 {
			b.mu.Unlock()
			unlock()
			slog.Info("party_rejected", "character", q.Session.CharacterID, "action", q.Action, "reason", "target_not_online_in_world", "target", q.Name)
			return sink.WritePacket(protocol.PartyFailure(21))
		}
	}
	b.mu.Unlock()
	// Only party commands wait for SQL; the presence mutex is released so
	// movement/map/stat refreshes never wait on this database round trip.
	writeCtx, done := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.MutateParty(writeCtx, q)
	done()
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return err
		}
		code := uint8(21)
		if errors.Is(err, domain.ErrPartyFull) {
			code = 22
		}
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrPartyInvite) || errors.Is(err, domain.ErrPartyFull) {
			unlock()
			slog.Info("party_rejected", "character", q.Session.CharacterID, "action", q.Action, "target", q.TargetID, "code", code, "error", err.Error())
			return sink.WritePacket(protocol.PartyFailure(code))
		}
		return err
	}
	b.mu.Lock()
	var out []partyDelivery
	// Cache committed membership before deriving runtime fields. In particular,
	// the joining peer becomes visible only after the durable accept commits.
	for _, m := range result.Before.Members {
		if p := b.peers[m.CharacterID]; p != nil {
			if _, remains := result.After.Member(m.CharacterID); !remains {
				p.hpSeen = nil
			}
			p.party = domain.PartySnapshot{}
		}
	}
	for _, m := range result.After.Members {
		if p := b.peers[m.CharacterID]; p != nil {
			p.party = result.After
		}
	}
	after := b.visible(result.After)
	for _, m := range after.Members {
		if p := b.peers[m.CharacterID]; p != nil && p.party.ID == after.ID {
			p.party = after
		}
	}
	switch q.Action {
	case domain.PartyCreate:
		out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.PartyCreated(after.ID), protocol.PartySnapshotPacket(partyPortalsFor(after, q.Session.CharacterID))}})
	case domain.PartyInvite:
		if result.Created {
			out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.PartyCreated(after.ID), protocol.PartySnapshotPacket(partyPortalsFor(after, q.Session.CharacterID))}})
		}
		if result.Invitation != nil {
			i := *result.Invitation
			c := q.Session.Character
			i.InviterName, i.InviterLevel, i.InviterJob, i.InviterSubJob = c.Name, c.Level, c.Job, c.SubJob
			out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.PartyInvited(q.Name)}})
			if target := b.peers[i.TargetID]; target != nil {
				out = append(out, partyDelivery{peer: target, packets: [][]byte{protocol.PartyInvitationPacket(i)}})
			}
		}
	case domain.PartyAccept:
		_, already := result.Before.Member(q.Session.CharacterID)
		for _, m := range after.Members {
			if p := b.peers[m.CharacterID]; p != nil {
				seen := partyPortalsFor(after, m.CharacterID)
				packet := protocol.PartySnapshotPacket(seen)
				if !already {
					packet = protocol.PartyJoined(seen, q.Session.Character.Name)
				}
				out = append(out, partyDelivery{peer: p, packets: [][]byte{packet}})
			}
		}
	case domain.PartyLeave, domain.PartyExpel:
		if result.Before.ID != 0 {
			for _, m := range result.Before.Members {
				if p := b.peers[m.CharacterID]; p != nil {
					packet := protocol.PartyDeparted(result.Before, partyPortalsFor(after, m.CharacterID), result.TargetID, q.Action == domain.PartyExpel)
					out = append(out, partyDelivery{peer: p, packets: [][]byte{packet}})
				}
			}
		}
	case domain.PartyLeader:
		for _, m := range after.Members {
			if p := b.peers[m.CharacterID]; p != nil {
				packet := protocol.PartySnapshotPacket(partyPortalsFor(after, m.CharacterID))
				out = append(out, partyDelivery{peer: p, packets: [][]byte{protocol.PartyLeaderChanged(after.LeaderID), packet}})
			}
		}
	case domain.PartySetting:
		out = append(out, b.snapshots(after)...)
	}
	out = append(out, s.partyHPDeltas(after)...)
	out = append(out, b.expeditionAfterParty(q, result)...)
	s.eventPartyChanged(result.Before, after)
	if q.Action == domain.PartyLeave || q.Action == domain.PartyExpel {
		s.eventExpeditionChangedLocked()
	}
	queueParty(out)
	b.mu.Unlock()
	unlock()
	if label, known := partyMonitorLabels[q.Action]; known {
		text := label
		if q.Action == domain.PartyInvite || q.Action == domain.PartyExpel || q.Action == domain.PartyLeader {
			text += " " + q.Name
		}
		monitor.record("party", q.Session, text+"（隊伍 "+strconv.FormatInt(max(result.After.ID, result.Before.ID), 10)+"，"+strconv.Itoa(len(result.After.Members))+" 人）")
	}
	// A recipient disconnect cannot turn a committed join into a client retry
	// which adds another member. Its own close path and reentry repair presence.
	return nil
}
