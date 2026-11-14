package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type tradeParticipant struct {
	id         int64
	live       *liveCharacter
	sink       packetSink
	space      *tradeSpace
	owner      domain.CharacterSession
	queue      chan tradeDelivery
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	retireOnce sync.Once
	retiring   bool                         // guarded by tradeSpace.mu; drain existing source frames for handoff
	failed     bool                         // guarded by tradeSpace.mu
	abandoned  []protocol.PacketPublication // guarded by tradeSpace.mu; abort outside it
}
type tradeRoom struct {
	id           string
	reference    uint32
	participants [2]*tradeParticipant
	offers       [2]domain.TradeOffer
	confirmed    [2]bool
	accepted     bool
	settling     bool
	closed       bool
}
type tradeSpace struct {
	mu            sync.Mutex
	clients       map[int64]*tradeParticipant
	rooms         map[int64]*tradeRoom
	nextReference uint32
}

func newTradeSpace() *tradeSpace {
	return &tradeSpace{clients: make(map[int64]*tradeParticipant), rooms: make(map[int64]*tradeRoom)}
}

func (s *Server) tradeEnter(live *liveCharacter, c packetSink) {
	if s.trades == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.trades.mu.Lock()
	var packets []tradePacket
	var retired *tradeParticipant
	if old := s.trades.clients[id]; old != nil {
		if old.live == live && sameDropSink(old.sink, c) {
			s.trades.mu.Unlock()
			return
		}
		if room := s.trades.rooms[id]; room != nil && !room.settling {
			packets = closeTradeLocked(s.trades, room, 2)
		}
		retired = old
		old.halt()
	}
	live.mu.Lock()
	owner := live.session
	live.mu.Unlock()
	peer := newTradeParticipant(s.trades, live, c, owner)
	s.trades.clients[id] = peer
	failed := s.trades.queueLocked(packets)
	s.trades.mu.Unlock()
	closeTradePeers(failed)
	if retired != nil && !sameDropSink(retired.sink, c) {
		go retired.closeSink()
	}
}

// tradeActive reports whether live is in a trade or seated in a hired
// merchant dialog; both lock the inventory against other commands.
func (s *Server) tradeActive(live *liveCharacter) bool {
	if s.merchantInside(live) {
		return true
	}
	if s.trades == nil {
		return false
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.trades.mu.Lock()
	defer s.trades.mu.Unlock()
	room := s.trades.rooms[id]
	return room != nil && !room.closed
}

type tradePacket struct {
	peer   *tradeParticipant
	packet []byte
}
type tradeDelivery struct {
	packet      []byte
	publication protocol.PacketPublication
}
type tradePublicationSink interface {
	ReservePublication() (protocol.PacketPublication, error)
}

const tradeOutputCapacity = 64

var errTradeOutput = errors.New("trade recipient queue failed closed")

func newTradeParticipant(t *tradeSpace, live *liveCharacter, sink packetSink, owner domain.CharacterSession) *tradeParticipant {
	owner.Character = domain.Character{} // fence identity only; never retain a stale asset snapshot
	p := &tradeParticipant{id: owner.CharacterID, live: live, sink: sink, space: t, owner: owner, queue: make(chan tradeDelivery, tradeOutputCapacity), stop: make(chan struct{}), done: make(chan struct{})}
	go p.writeLoop()
	return p
}
func (p *tradeParticipant) halt() { p.stopOnce.Do(func() { close(p.stop) }) }
func (p *tradeParticipant) retireLocked() {
	p.retiring = true
	p.retireOnce.Do(func() { close(p.queue) })
}
func (p *tradeParticipant) closeSink() {
	p.space.mu.Lock()
	abandoned := p.abandoned
	p.abandoned = nil
	p.space.mu.Unlock()
	if c, ok := p.sink.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	for _, slot := range abandoned {
		slot.Abort(errTradeOutput)
	}
	for {
		select {
		case d, ok := <-p.queue:
			if !ok {
				return
			}
			if d.publication != nil {
				d.publication.Abort(errTradeOutput)
			}
		default:
			return
		}
	}
}
func closeTradePeers(peers []*tradeParticipant) {
	for _, p := range peers {
		go p.closeSink()
	}
}
func (p *tradeParticipant) ownerCurrentLocked() bool {
	current := p.space.clients[p.id]
	if p.failed || (current != p && !(p.retiring && current == nil)) {
		return false
	}
	p.live.mu.Lock()
	q := p.live.session
	p.live.mu.Unlock()
	return q.CharacterID == p.owner.CharacterID && q.SessionID == p.owner.SessionID && q.Generation == p.owner.Generation && q.ChannelID == p.owner.ChannelID && q.Lease.WorldID == p.owner.Lease.WorldID && q.Lease.InstanceID == p.owner.Lease.InstanceID && q.Lease.Epoch == p.owner.Lease.Epoch
}
func (t *tradeSpace) breakPeerLocked(p *tradeParticipant) []tradePacket {
	if p.failed {
		return nil
	}
	p.failed = true
	p.halt()
	if t.clients[p.id] != p {
		return nil
	}
	delete(t.clients, p.id)
	if room := t.rooms[p.id]; room != nil && !room.settling {
		return closeTradeLocked(t, room, 2)
	}
	return nil
}

// Every producer enqueues while holding the authority mutex, preserving the
// same per-recipient order as room state changes. No socket write/close/wait
// occurs under it. Saturation invalidates the peer and provisional room, rather
// than dropping an essential open/item/terminal frame and continuing the trade.
func (t *tradeSpace) queueLocked(packets []tradePacket) []*tradeParticipant {
	pending := append([]tradePacket(nil), packets...)
	var failed []*tradeParticipant
	for n := 0; n < len(pending); n++ {
		d := pending[n]
		p := d.peer
		if p == nil || d.packet == nil {
			continue
		}
		if t.clients[p.id] != p {
			if !p.retiring {
				p.halt()
			}
			continue
		} // obsolete socket; never address its replacement
		bad := !p.ownerCurrentLocked() || p.retiring
		delivery := tradeDelivery{packet: append([]byte(nil), d.packet...)}
		if !bad {
			if sink, ok := p.sink.(tradePublicationSink); ok {
				var err error
				delivery.publication, err = sink.ReservePublication()
				if err != nil {
					bad = true
				}
			}
		}
		if !bad {
			select {
			case <-p.stop:
				bad = true
			default:
				select {
				case p.queue <- delivery:
				default:
					bad = true
				}
			}
		}
		if bad && !p.failed {
			if delivery.publication != nil {
				p.abandoned = append(p.abandoned, delivery.publication)
			}
			pending = append(pending, t.breakPeerLocked(p)...)
			failed = append(failed, p)
		}
	}
	return failed
}
func (p *tradeParticipant) writeLoop() {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		select {
		case <-p.stop:
			return
		case delivery, ok := <-p.queue:
			if !ok {
				return
			}
			p.space.mu.Lock()
			valid := p.ownerCurrentLocked()
			p.space.mu.Unlock()
			if !valid {
				if delivery.publication != nil {
					delivery.publication.Abort(errTradeOutput)
				}
				p.fail()
				return
			}
			var err error
			if delivery.publication != nil {
				err = delivery.publication.WritePackets([][]byte{delivery.packet})
			} else {
				err = p.sink.WritePacket(delivery.packet)
			}
			if err != nil {
				p.fail()
				return
			}
		}
	}
}
func (p *tradeParticipant) fail() {
	p.space.mu.Lock()
	packets := p.space.breakPeerLocked(p)
	failed := p.space.queueLocked(packets)
	p.space.mu.Unlock()
	p.halt()
	p.closeSink()
	closeTradePeers(failed)
}

// Closing a trade only discards provisional offers. The authority never moves
// inventory or money until the single durable settlement transaction commits.
func (s *Server) tradeLeave(live *liveCharacter) {
	if s.trades == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	t := s.trades
	t.mu.Lock()
	var packets []tradePacket
	if current := t.clients[id]; current != nil && current.live == live {
		delete(t.clients, id)
		current.retireLocked()
		if room := t.rooms[id]; room != nil {
			// Both confirmations are a commitment. A disconnect cannot announce
			// cancellation while the atomic settlement may already be committing.
			// Fencing decides whether settlement or session release wins.
			if !room.settling {
				packets = closeTradeLocked(t, room, 2)
			}
		}
	}
	failed := t.queueLocked(packets)
	t.mu.Unlock()
	closeTradePeers(failed)
}

func closeTradeLocked(t *tradeSpace, room *tradeRoom, reason byte) []tradePacket {
	room.closed = true
	packets := make([]tradePacket, 0, 2)
	for seat, p := range room.participants {
		if p == nil {
			continue
		}
		if t.rooms[p.id] == room {
			delete(t.rooms, p.id)
		}
		packets = append(packets, tradePacket{p, protocol.TradeClose(byte(seat), reason)})
	}
	return packets
}

func tradeCharacter(p *tradeParticipant) domain.Character {
	p.live.mu.Lock()
	defer p.live.mu.Unlock()
	return p.live.session.Character
}

func (s *Server) handleTrade(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	if s.trades == nil {
		return c.WritePacket(protocol.EnableActions())
	}
	request, err := protocol.ReadTradeRequest(r)
	if err != nil {
		if errors.Is(err, protocol.ErrUnsupportedTradeAction) {
			return c.WritePacket(protocol.EnableActions())
		}
		return err
	}
	action := request.Action
	// Native busy rejection is an invitation reply too. It must be able to
	// remove a pending room even while a storage/shop/NPC dialog is active.
	if action != 12 && action != 18 && s.tradeDialogBusy(live) {
		return c.WritePacket(protocol.EnableActions())
	}
	targetID := request.TargetID
	invType, slot, quantity, tradeSlot := request.InvType, request.Slot, request.Quantity, request.TradeSlot
	mesos := request.Mesos
	live.mu.Lock()
	id := live.session.CharacterID
	dead := live.session.Character.HP <= 0
	live.mu.Unlock()
	if dead && action != 12 && action != 18 && action != 5 {
		return c.WritePacket(protocol.EnableActions())
	}
	t := s.trades
	t.mu.Lock()
	self := t.clients[id]
	room := t.rooms[id]
	packets := make([]tradePacket, 0, 4)
	settle := false
	if self == nil || self.live != live || !sameDropSink(self.sink, c) {
		t.mu.Unlock()
		return c.WritePacket(protocol.EnableActions())
	}
	seat := -1
	if room != nil {
		for i, p := range room.participants {
			if p == self {
				seat = i
				break
			}
		}
	}
	if action == 6 && room == nil {
		// Native reference is a positive I32. Never wrap within a process: old
		// invitation replies must not identify a newly allocated room.
		if t.nextReference >= 2147483647 {
			t.mu.Unlock()
			return c.WritePacket(protocol.EnableActions())
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.mu.Unlock()
			return err
		}
		t.nextReference++
		room = &tradeRoom{id: hex.EncodeToString(random[:]), reference: t.nextReference}
		room.participants[0] = self
		t.rooms[id] = room
		packets = append(packets, tradePacket{self, protocol.TradeOpen(tradeCharacter(self), nil, 0)})
	} else if room == nil || room.closed || room.settling || seat < 0 {
		packets = append(packets, tradePacket{self, protocol.EnableActions()})
	} else {
		other := room.participants[1-seat]
		switch action {
		case 11:
			candidate := t.clients[targetID]
			if seat != 0 || other != nil || candidate == nil || candidate == self || t.rooms[targetID] != nil {
				packets = append(packets, tradePacket{self, protocol.EnableActions()})
				break
			}
			if s.tradeDialogBusy(candidate.live) {
				packets = append(packets, tradePacket{self, protocol.EnableActions()})
				break
			}
			mine, them := tradeCharacter(self), tradeCharacter(candidate)
			self.live.mu.Lock()
			mySession := self.live.session
			self.live.mu.Unlock()
			candidate.live.mu.Lock()
			theirSession := candidate.live.session
			candidate.live.mu.Unlock()
			if mine.HP <= 0 || them.HP <= 0 || mine.AccountID == them.AccountID || mySession.ChannelID != theirSession.ChannelID || mine.MapID != them.MapID || !within(mine.X, mine.Y, them.X, them.Y, 800) {
				packets = append(packets, tradePacket{self, protocol.EnableActions()})
				break
			}
			room.participants[1] = candidate
			t.rooms[targetID] = room
			packets = append(packets, tradePacket{candidate, protocol.TradeInvite(mine.Name, room.reference)})
		case 9:
			if seat != 1 || other == nil || room.accepted || request.Reference != room.reference {
				break
			}
			room.accepted = true
			mine, them := tradeCharacter(self), tradeCharacter(other)
			packets = append(packets, tradePacket{other, protocol.TradeVisitor(mine)}, tradePacket{self, protocol.TradeOpen(mine, &them, 1)})
		case 12:
			if seat == 1 && !room.accepted && request.Reference == room.reference {
				packets = append(packets, closeTradeLocked(t, room, 2)...)
			}
		case 18:
			packets = append(packets, closeTradeLocked(t, room, 2)...)
		case 0:
			if !room.accepted || other == nil || room.confirmed[0] || room.confirmed[1] || len(room.offers[seat].Items) >= 9 {
				break
			}
			mine := tradeCharacter(self)
			offered, ok := buildTradeItem(mine, room.offers[seat], invType, slot, quantity, tradeSlot)
			if !ok {
				packets = append(packets, tradePacket{self, protocol.EnableActions()})
				break
			}
			room.offers[seat].Items = append(room.offers[seat].Items, offered)
			packets = append(packets, tradePacket{self, protocol.TradeItem(0, offered)}, tradePacket{other, protocol.TradeItem(1, offered)})
		case 1:
			if !room.accepted || other == nil || room.confirmed[0] || room.confirmed[1] || mesos <= 0 {
				break
			}
			mine := tradeCharacter(self)
			total := room.offers[seat].Mesos + mesos
			if total < 0 || total > mine.Mesos {
				packets = append(packets, tradePacket{self, protocol.EnableActions()})
				break
			}
			room.offers[seat].Mesos = total
			packets = append(packets, tradePacket{self, protocol.TradeMesos(0, total)}, tradePacket{other, protocol.TradeMesos(1, total)})
		case 2:
			if !room.accepted || other == nil || room.confirmed[seat] || !tradeChecksMatch(room.offers[seat], request.Checks) {
				break
			}
			room.confirmed[seat] = true
			packets = append(packets, tradePacket{other, protocol.TradeConfirm()})
			if room.confirmed[0] && room.confirmed[1] {
				room.settling = true
				settle = true
			}
		case 5:
			// The native peer-confirm decoder automatically acknowledges its
			// observed remote item-info list. This does not confirm the actor or
			// authorize settlement. Only its own original action2 does that.
			if !room.accepted || other == nil || !tradeChecksMatch(room.offers[1-seat], request.Checks) {
				break
			}
		}
	}
	failed := t.queueLocked(packets)
	t.mu.Unlock()
	closeTradePeers(failed)
	if settle {
		if err := s.settleTrade(ctx, room); err != nil {
			return err
		}
	}
	for _, p := range failed {
		if p == self {
			return errTradeOutput
		}
	}
	return nil
}

func (s *Server) tradeDialogBusy(live *liveCharacter) bool {
	if s.getEconomyDialog(live, "storage") != nil || s.getEconomyDialog(live, "shop") != nil {
		return true
	}
	if s.scripts == nil {
		return false
	}
	live.mu.Lock()
	id := live.session.CharacterID
	identitySession, identityGeneration := live.session.SessionID, live.session.Generation
	live.mu.Unlock()
	s.scripts.mu.Lock()
	active := s.scripts.active[id]
	busy := active != nil && active.live == live && active.identity.CharacterID == id && active.identity.SessionID == identitySession && active.identity.Generation == identityGeneration
	s.scripts.mu.Unlock()
	return busy
}

func tradeChecksMatch(offer domain.TradeOffer, checks []protocol.TradeCheck) bool {
	if len(offer.Items) != len(checks) {
		return false
	}
	want := make([]int, 0, len(offer.Items))
	got := make([]int, 0, len(checks))
	for _, item := range offer.Items {
		if item.Equipment != nil {
			want = append(want, item.Equipment.ItemID)
		} else if item.Item != nil {
			want = append(want, item.Item.TemplateID)
		} else {
			return false
		}
	}
	for _, check := range checks {
		got = append(got, check.ItemID)
	}
	sort.Ints(want)
	sort.Ints(got)
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

func buildTradeItem(c domain.Character, offer domain.TradeOffer, invType, slot, quantity, target int) (domain.TradeItem, bool) {
	// The cash tab holds only cash items, which Tradeable gates below.
	if invType < 1 || invType > 5 || slot < 1 || slot > c.Capacity(invType) || quantity < 1 {
		return domain.TradeItem{}, false
	}
	used := make(map[int]bool)
	reserved := 0
	for _, item := range offer.Items {
		used[item.TradeSlot] = true
		if item.InvType == invType && item.Slot == slot {
			reserved += item.Quantity
		}
	}
	if target < 1 || target > 9 || used[target] {
		target = 0
		for n := 1; n <= 9; n++ {
			if !used[n] {
				target = n
				break
			}
		}
	}
	if target == 0 {
		return domain.TradeItem{}, false
	}
	result := domain.TradeItem{InvType: invType, Slot: slot, Quantity: quantity, TradeSlot: target}
	if invType == 1 {
		for _, e := range c.Equipment {
			if e.Slot == slot && quantity == 1 && reserved == 0 {
				definition, err := content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), e.ItemID)
				if err != nil || !definition.Tradeable(e.CashID) {
					return domain.TradeItem{}, false
				}
				copy := e
				result.Equipment = &copy
				result.InstanceID = e.ID
				return result, true
			}
		}
	} else {
		for _, item := range c.Items {
			if item.InvType == invType && item.Slot == slot && quantity+reserved <= item.Quantity {
				definition, err := content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), item.TemplateID)
				// A cash stack is one serial: it is offered once.
				if err != nil || !definition.Tradeable(item.CashID) || (item.CashID > 0 && reserved > 0) {
					return domain.TradeItem{}, false
				}
				copy := item
				result.Item = &copy
				result.InstanceID = item.ID
				return result, true
			}
		}
	}
	return domain.TradeItem{}, false
}

func (s *Server) settleTrade(ctx context.Context, room *tradeRoom) error {
	first, second := room.participants[0], room.participants[1]
	if first == nil || second == nil {
		return nil
	}
	a, b := first.live, second.live
	a.mu.Lock()
	aid := a.session.CharacterID
	a.mu.Unlock()
	b.mu.Lock()
	bid := b.session.CharacterID
	b.mu.Unlock()
	if aid < bid {
		a.persistMu.Lock()
		b.persistMu.Lock()
		defer a.persistMu.Unlock()
		defer b.persistMu.Unlock()
	} else {
		b.persistMu.Lock()
		a.persistMu.Lock()
		defer b.persistMu.Unlock()
		defer a.persistMu.Unlock()
	}
	t := s.trades
	t.mu.Lock()
	if room.closed {
		t.mu.Unlock()
		return nil
	}
	offers := room.offers
	t.mu.Unlock()
	a.mu.Lock()
	a.session.Lease = s.currentLease()
	firstSession := a.session
	a.mu.Unlock()
	b.mu.Lock()
	b.session.Lease = s.currentLease()
	secondSession := b.session
	b.mu.Unlock()
	repo, ok := s.repo.(domain.TradeRepository)
	var result domain.TradeResult
	var err error
	if ok {
		result, err = repo.SettleTrade(ctx, domain.TradeSettlement{ID: room.id, First: firstSession, Second: secondSession, FirstOffer: offers[0], SecondOffer: offers[1]})
	} else {
		err = domain.ErrForbidden
	}
	if err != nil && !tradeDefinitiveFailure(err) {
		resolver, canResolve := s.repo.(domain.TradeReceiptRepository)
		if canResolve {
			resolveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			resolved, committed, resolveErr := resolver.ResolveTradeReceipt(resolveCtx, domain.TradeSettlement{ID: room.id, First: firstSession, Second: secondSession, FirstOffer: offers[0], SecondOffer: offers[1]})
			cancel()
			if resolveErr == nil && committed {
				result = resolved
				err = nil
			} else if resolveErr != nil {
				return s.tradeUnknownOutcome(room, resolveErr)
			}
			// A lock-serialized authoritative absence means the old transaction
			// rolled back. It may now truthfully receive the ordinary failure.
		} else {
			return s.tradeUnknownOutcome(room, err)
		}
	}
	packets := make([]tradePacket, 0, 6)
	if err == nil {
		monitor.record("trade", firstSession, "與 "+secondSession.Character.Name+" 完成交易：給出 "+tradeMonitorOffer(offers[0])+"，收到 "+tradeMonitorOffer(offers[1]))
		s.applyInventoryResult(a, result.First.Character)
		s.applyInventoryResult(b, result.Second.Character)
		for i, p := range []*tradeParticipant{first, second} {
			state := result.First
			if i == 1 {
				state = result.Second
			}
			if len(state.Changes) > 0 {
				packets = append(packets, tradePacket{p, protocol.ModifyInventory(state.Changes)})
			}
			packets = append(packets, tradePacket{p, protocol.UpdateMesos(state.Character.Mesos, true)})
		}
	}
	t.mu.Lock()
	reason := byte(7)
	if err != nil {
		reason = 8
	}
	packets = append(packets, closeTradeLocked(t, room, reason)...)
	failed := t.queueLocked(packets)
	t.mu.Unlock()
	closeTradePeers(failed)
	return nil
}

func tradeMonitorOffer(offer domain.TradeOffer) string {
	return strconv.FormatInt(offer.Mesos, 10) + " 楓幣與 " + strconv.Itoa(len(offer.Items)) + " 件道具"
}

func tradeDefinitiveFailure(err error) bool {
	return errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrStaleOwner)
}
func (s *Server) tradeUnknownOutcome(room *tradeRoom, cause error) error {
	t := s.trades
	t.mu.Lock()
	room.closed = true
	var failed []*tradeParticipant
	for _, p := range room.participants {
		if p == nil {
			continue
		}
		if t.rooms[p.id] == room {
			delete(t.rooms, p.id)
		}
		t.breakPeerLocked(p)
		failed = append(failed, p)
	}
	t.mu.Unlock()
	closeTradePeers(failed)
	return errors.Join(errors.New("trade commit outcome unresolved; reconnect to reload authoritative assets"), cause)
}
