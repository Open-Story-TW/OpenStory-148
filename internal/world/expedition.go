package world

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// An expedition (遠征隊) groups up to five ordinary parties under one leader.
// The parties stay durable party rows; the grouping lives in this world
// process only, like the Legacy server's, and ends with the process. Every
// expedition change runs under partySpace.commands, so it is ordered with
// party commands, and its packets go through the party peers' writers.
type expedition struct {
	id      int64
	kind    int
	leader  int64
	parties []int64 // party ids in status order
	// The last committed snapshot of each party; visible() adds presence.
	snapshots map[int64]domain.PartySnapshot
}

type expeditionInvite struct {
	exp       *expedition
	inviterID int64
	expires   time.Time
}

type expeditions struct {
	next    int64
	byParty map[int64]*expedition
	invites map[int64]expeditionInvite // invited character -> invitation
}

func newExpeditions() *expeditions {
	return &expeditions{byParty: map[int64]*expedition{}, invites: map[int64]expeditionInvite{}}
}

const expeditionInviteTTL = 60 * time.Second

func (e *expedition) index(partyID int64) int {
	for i, id := range e.parties {
		if id == partyID {
			return i
		}
	}
	return -1
}

// leaderIndex is the index of the party the leader is in; the client names
// the expedition leader after that party's leader.
func (e *expedition) leaderIndex() int {
	for i, id := range e.parties {
		if _, ok := e.snapshots[id].Member(e.leader); ok {
			return i
		}
	}
	return 0
}

// syncLeader keeps the expedition leader a party leader, as the client
// shows it: when its party's leadership moved, or it left, the leader of its
// party (or of the first party) takes over. It reports a change.
func (e *expedition) syncLeader() bool {
	if len(e.parties) == 0 {
		return false
	}
	want := e.snapshots[e.parties[0]].LeaderID
	for _, id := range e.parties {
		if p := e.snapshots[id]; p.LeaderID != 0 {
			if _, ok := p.Member(e.leader); ok {
				want = p.LeaderID
				break
			}
		}
	}
	if want == e.leader || want == 0 {
		return false
	}
	e.leader = want
	return true
}

func (e *expedition) memberCount() int {
	n := 0
	for _, id := range e.parties {
		n += len(e.snapshots[id].Members)
	}
	return n
}

// partyOf finds the party holding a character.
func (e *expedition) partyOf(id int64) (domain.PartySnapshot, bool) {
	for _, pid := range e.parties {
		p := e.snapshots[pid]
		if _, ok := p.Member(id); ok {
			return p, true
		}
	}
	return domain.PartySnapshot{}, false
}

func (b *partySpace) expeditionParties(e *expedition) []domain.PartySnapshot {
	out := make([]domain.PartySnapshot, 0, len(e.parties))
	for _, id := range e.parties {
		out = append(out, b.visible(e.snapshots[id]))
	}
	return out
}

// expeditionPeers lists the online members of an expedition.
func (b *partySpace) expeditionPeers(e *expedition) []*partyPeer {
	var out []*partyPeer
	for _, pid := range e.parties {
		for _, m := range e.snapshots[pid].Members {
			if p := b.peers[m.CharacterID]; p != nil && p.party.ID == pid {
				out = append(out, p)
			}
		}
	}
	return out
}

func (b *partySpace) expeditionBroadcast(e *expedition, skip int64, packets ...[]byte) []partyDelivery {
	var out []partyDelivery
	for _, p := range b.expeditionPeers(e) {
		if partySession(p.live).CharacterID != skip {
			out = append(out, partyDelivery{peer: p, packets: packets})
		}
	}
	return out
}

// expeditionPartyChanged records a committed party snapshot and tells every
// member about that party. An emptied party leaves the expedition (the client
// shifts the later indexes down itself, 0x4F7AD4).
func (b *partySpace) expeditionPartyChanged(e *expedition, before, after domain.PartySnapshot) []partyDelivery {
	id := before.ID
	if id == 0 {
		id = after.ID
	}
	i := e.index(id)
	if i < 0 {
		return nil
	}
	if after.ID == 0 {
		delete(e.snapshots, id)
		delete(b.exp.byParty, id)
		e.parties = append(e.parties[:i], e.parties[i+1:]...)
		after = domain.PartySnapshot{}
	} else {
		e.snapshots[id] = after
	}
	changed := e.syncLeader()
	out := b.expeditionBroadcast(e, 0, protocol.ExpeditionPartyUpdate(e.leaderIndex(), i, b.visible(after)))
	if changed {
		out = append(out, b.expeditionBroadcast(e, 0, protocol.ExpeditionLeaderChanged(e.leaderIndex()))...)
	}
	return out
}

func (b *partySpace) addExpeditionParty(e *expedition, p domain.PartySnapshot) {
	e.parties = append(e.parties, p.ID)
	e.snapshots[p.ID] = p
	b.exp.byParty[p.ID] = e
}

// commitParty runs one party mutation for an expedition and delivers the
// party packets it implies, as applyParty does for the wire actions. The
// caller holds b.commands and not b.mu.
func (s *Server) commitParty(ctx context.Context, repo domain.PartyRepository, q domain.PartyRequest) (domain.PartyMutation, []partyDelivery, error) {
	writeCtx, done := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.MutateParty(writeCtx, q)
	done()
	if err != nil {
		return result, nil, err
	}
	b := s.parties
	b.mu.Lock()
	defer b.mu.Unlock()
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
	var out []partyDelivery
	switch q.Action {
	case domain.PartyCreate:
		if p := b.peers[q.Session.CharacterID]; p != nil {
			out = append(out, partyDelivery{peer: p, packets: [][]byte{protocol.PartyCreated(after.ID), protocol.PartySnapshotPacket(after)}})
		}
	case domain.PartyJoin:
		packet := protocol.PartyJoined(after, q.Session.Character.Name)
		for _, m := range after.Members {
			if p := b.peers[m.CharacterID]; p != nil {
				out = append(out, partyDelivery{peer: p, packets: [][]byte{packet}})
			}
		}
	case domain.PartyRemove, domain.PartyDisband:
		packet := protocol.PartyDeparted(result.Before, after, result.TargetID, q.Action == domain.PartyRemove && result.TargetID != q.Session.CharacterID)
		for _, m := range result.Before.Members {
			if p := b.peers[m.CharacterID]; p != nil {
				out = append(out, partyDelivery{peer: p, packets: [][]byte{packet}})
			}
		}
		s.eventPartyChanged(result.Before, after)
		if q.Action == domain.PartyRemove && after.ID != 0 && after.LeaderID != result.Before.LeaderID {
			out = append(out, b.snapshots(after)...)
		}
	case domain.PartySetLeader:
		for _, m := range after.Members {
			if p := b.peers[m.CharacterID]; p != nil {
				out = append(out, partyDelivery{peer: p, packets: [][]byte{protocol.PartyLeaderChanged(after.LeaderID), protocol.PartySnapshotPacket(after)}})
			}
		}
	}
	out = append(out, s.partyHPDeltas(after)...)
	result.After = after
	return result, out, nil
}

func (s *Server) handleExpedition(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	q, err := protocol.ReadExpeditionRequest(r)
	if err != nil {
		return err
	}
	repo, ok := s.repo.(domain.PartyRepository)
	if !ok || s.parties == nil {
		return nil
	}
	b := s.parties
	b.commands.Lock()
	defer b.commands.Unlock()
	me := partySession(live)
	me.Lease = s.currentLease()
	b.mu.Lock()
	peer := b.peers[me.CharacterID]
	if peer == nil || peer.live != live {
		b.mu.Unlock()
		return domain.ErrStaleOwner
	}
	if !peer.budget.take(time.Now()) {
		b.mu.Unlock()
		return nil
	}
	exp := b.exp.byParty[peer.party.ID]
	b.mu.Unlock()
	var out []partyDelivery
	switch q.Mode {
	case protocol.ExpeditionCreate:
		out, err = s.expeditionCreate(ctx, repo, me, peer, q.Type)
	case protocol.ExpeditionInvite:
		out = s.expeditionInvite(me, peer, exp, q.Name)
	case protocol.ExpeditionReply:
		out, err = s.expeditionReply(ctx, repo, me, peer, q)
	case protocol.ExpeditionLeave:
		if exp != nil {
			if exp.leader == me.CharacterID {
				out, err = s.expeditionDisband(ctx, repo, me, exp)
			} else {
				out, err = s.expeditionRemove(ctx, repo, me, exp, me.CharacterID, false)
			}
		}
	case protocol.ExpeditionKick:
		if exp != nil && exp.leader == me.CharacterID && q.CharID != me.CharacterID {
			out, err = s.expeditionRemove(ctx, repo, me, exp, q.CharID, true)
		}
	case protocol.ExpeditionLeader:
		if exp != nil && exp.leader == me.CharacterID && q.CharID != me.CharacterID {
			out, err = s.expeditionLeader(ctx, repo, me, exp, q.CharID)
		}
	case protocol.ExpeditionPartyLeader:
		if exp != nil && exp.leader == me.CharacterID {
			out, err = s.expeditionPartyLeader(ctx, repo, me, exp, q.CharID)
		}
	case protocol.ExpeditionMove:
		if exp != nil && exp.leader == me.CharacterID {
			out, err = s.expeditionMove(ctx, repo, exp, q.CharID, q.PartyIndex)
		}
	}
	b.mu.Lock()
	queueParty(out)
	b.mu.Unlock()
	if err != nil && !errors.Is(err, domain.ErrStaleOwner) {
		slog.Info("expedition_rejected", "character", me.CharacterID, "mode", q.Mode, "error", err.Error())
		return nil
	}
	return err
}

func expeditionType(kind int) (content.ExpeditionType, bool) {
	return content.ExpeditionTypeByID(kind)
}

func (s *Server) expeditionCreate(ctx context.Context, repo domain.PartyRepository, me domain.CharacterSession, peer *partyPeer, kind int) ([]partyDelivery, error) {
	t, ok := expeditionType(kind)
	name := me.Character.Name
	if !ok {
		return []partyDelivery{{peer: peer, packets: [][]byte{protocol.ExpeditionMessage(protocol.ExpeditionMsgLevel, name)}}}, nil
	}
	if lv := me.Character.Level; lv < t.LevelMin || lv > t.LevelMax {
		return []partyDelivery{{peer: peer, packets: [][]byte{protocol.ExpeditionMessage(protocol.ExpeditionMsgLevel, name)}}}, nil
	}
	b := s.parties
	b.mu.Lock()
	inParty := peer.party.ID != 0
	b.mu.Unlock()
	if inParty {
		return []partyDelivery{{peer: peer, packets: [][]byte{protocol.ExpeditionMessage(protocol.ExpeditionMsgInParty, name)}}}, nil
	}
	result, out, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartyCreate})
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exp.next++
	e := &expedition{id: b.exp.next, kind: t.ID, leader: me.CharacterID, snapshots: map[int64]domain.PartySnapshot{}}
	b.addExpeditionParty(e, result.After)
	out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.ExpeditionStatus(e.kind, e.leaderIndex(), b.expeditionParties(e), true, false)}})
	monitor.record("party", me, "建立遠征隊 "+t.Name+"（"+strconv.Itoa(t.ID)+"）")
	return out, nil
}

func (s *Server) expeditionInvite(me domain.CharacterSession, peer *partyPeer, e *expedition, name string) []partyDelivery {
	msg := func(code int, who string) []partyDelivery {
		return []partyDelivery{{peer: peer, packets: [][]byte{protocol.ExpeditionMessage(code, who)}}}
	}
	if e == nil || e.leader != me.CharacterID {
		return nil
	}
	t, ok := expeditionType(e.kind)
	if !ok {
		return nil
	}
	b := s.parties
	b.mu.Lock()
	defer b.mu.Unlock()
	var target *partyPeer
	var ts domain.CharacterSession
	for _, p := range b.peers {
		c := partySession(p.live)
		if c.Lease.WorldID == me.Lease.WorldID && strings.EqualFold(c.Character.Name, name) {
			target, ts = p, c
			break
		}
	}
	if target == nil {
		return msg(protocol.ExpeditionMsgNotFound, name)
	}
	name = ts.Character.Name
	if target.party.ID != 0 {
		return msg(protocol.ExpeditionMsgInParty, name)
	}
	if lv := ts.Character.Level; lv < t.LevelMin || lv > t.LevelMax {
		return msg(protocol.ExpeditionMsgLevel, name)
	}
	if e.memberCount() >= t.Users {
		return nil
	}
	if old, ok := b.exp.invites[ts.CharacterID]; ok && old.exp == e && time.Now().Before(old.expires) {
		return msg(protocol.ExpeditionMsgAlreadyInvite, name)
	}
	b.exp.invites[ts.CharacterID] = expeditionInvite{exp: e, inviterID: me.CharacterID, expires: time.Now().Add(expeditionInviteTTL)}
	c := me.Character
	return []partyDelivery{{peer: target, packets: [][]byte{protocol.ExpeditionInvitation(c.Level, c.Job, c.SubJob, c.Name, e.kind)}}}
}

func (s *Server) expeditionReply(ctx context.Context, repo domain.PartyRepository, me domain.CharacterSession, peer *partyPeer, q protocol.ExpeditionRequest) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	inv, ok := b.exp.invites[me.CharacterID]
	var inviter *partyPeer
	if ok {
		inviter = b.peers[inv.inviterID]
	}
	if !ok || time.Now().After(inv.expires) || inviter == nil || !strings.EqualFold(partySession(inviter.live).Character.Name, q.Name) {
		delete(b.exp.invites, me.CharacterID)
		b.mu.Unlock()
		return nil, nil
	}
	name := me.Character.Name
	tell := func(code int) []partyDelivery {
		return []partyDelivery{{peer: inviter, packets: [][]byte{protocol.ExpeditionMessage(code, name)}}}
	}
	switch q.Code {
	case protocol.ExpeditionReplyShown:
		b.mu.Unlock()
		return tell(protocol.ExpeditionMsgInvited), nil
	case protocol.ExpeditionReplyRefusing, protocol.ExpeditionReplyBusy, protocol.ExpeditionReplyPending:
		delete(b.exp.invites, me.CharacterID)
		b.mu.Unlock()
		return tell(q.Code), nil
	case protocol.ExpeditionReplyDeny:
		delete(b.exp.invites, me.CharacterID)
		b.mu.Unlock()
		return nil, nil
	}
	delete(b.exp.invites, me.CharacterID)
	e := inv.exp
	if b.exp.byParty[firstParty(e)] != e || peer.party.ID != 0 {
		b.mu.Unlock()
		return tell(protocol.ExpeditionMsgInParty), nil
	}
	t, okType := expeditionType(e.kind)
	if !okType || e.memberCount() >= t.Users {
		b.mu.Unlock()
		return nil, nil
	}
	target := int64(0)
	for _, pid := range e.parties {
		if len(e.snapshots[pid].Members) < domain.PartyCapacity {
			target = pid
			break
		}
	}
	room := len(e.parties) < t.MaxParties()
	b.mu.Unlock()
	if target == 0 && !room {
		return nil, nil
	}
	var result domain.PartyMutation
	var out []partyDelivery
	var err error
	if target != 0 {
		result, out, err = s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartyJoin, PartyID: target})
	} else {
		result, out, err = s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartyCreate})
	}
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if target == 0 {
		b.addExpeditionParty(e, result.After)
	} else {
		e.snapshots[target] = result.After
	}
	i := e.index(result.After.ID)
	others := b.expeditionBroadcast(e, me.CharacterID, protocol.ExpeditionJoined(name), protocol.ExpeditionPartyUpdate(e.leaderIndex(), i, result.After))
	out = append(out, others...)
	out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.ExpeditionStatus(e.kind, e.leaderIndex(), b.expeditionParties(e), false, true)}})
	monitor.record("party", me, "加入遠征隊（"+strconv.Itoa(e.kind)+"）")
	return out, nil
}

func firstParty(e *expedition) int64 {
	if len(e.parties) == 0 {
		return 0
	}
	return e.parties[0]
}

// expeditionRemove takes one member out: the member leaving (kicked=false)
// or the leader kicking it.
func (s *Server) expeditionRemove(ctx context.Context, repo domain.PartyRepository, actor domain.CharacterSession, e *expedition, target int64, kicked bool) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	p, ok := e.partyOf(target)
	m, _ := p.Member(target)
	targetPeer := b.peers[target]
	b.mu.Unlock()
	if !ok {
		return nil, nil
	}
	result, out, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: actor, Action: domain.PartyRemove, PartyID: p.ID, TargetID: target})
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if targetPeer != nil && targetPeer.party.ID == 0 {
		reason := "left"
		if kicked {
			reason = "kicked"
		}
		out = append(out, partyDelivery{peer: targetPeer, packets: [][]byte{protocol.ExpeditionRemoved(reason)}})
	}
	notice := protocol.ExpeditionLeft(m.Name)
	if kicked {
		notice = protocol.ExpeditionKicked(m.Name)
	}
	out = append(out, b.expeditionBroadcast(e, target, notice)...)
	out = append(out, b.expeditionPartyChanged(e, result.Before, result.After)...)
	return out, nil
}

func (s *Server) expeditionDisband(ctx context.Context, repo domain.PartyRepository, me domain.CharacterSession, e *expedition) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	peers := b.expeditionPeers(e)
	parties := append([]int64(nil), e.parties...)
	b.mu.Unlock()
	var out []partyDelivery
	for _, pid := range parties {
		_, more, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartyDisband, PartyID: pid})
		out = append(out, more...)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return out, err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pid := range parties {
		delete(b.exp.byParty, pid)
	}
	for id, inv := range b.exp.invites {
		if inv.exp == e {
			delete(b.exp.invites, id)
		}
	}
	for _, p := range peers {
		out = append(out, partyDelivery{peer: p, packets: [][]byte{protocol.ExpeditionRemoved("disbanded")}})
	}
	monitor.record("party", me, "解散遠征隊（"+strconv.Itoa(e.kind)+"）")
	return out, nil
}

// expeditionLeader hands the expedition to a member, making it the leader of
// its party first, because the client names the expedition leader by party.
func (s *Server) expeditionLeader(ctx context.Context, repo domain.PartyRepository, me domain.CharacterSession, e *expedition, target int64) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	p, ok := e.partyOf(target)
	b.mu.Unlock()
	if !ok {
		return nil, nil
	}
	var out []partyDelivery
	if p.LeaderID != target {
		result, more, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartySetLeader, PartyID: p.ID, TargetID: target})
		out = more
		if err != nil {
			return out, err
		}
		b.mu.Lock()
		e.leader = target
		out = append(out, b.expeditionPartyChanged(e, result.Before, result.After)...)
		b.mu.Unlock()
	} else {
		e.leader = target
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out = append(out, b.expeditionBroadcast(e, 0, protocol.ExpeditionLeaderChanged(e.leaderIndex()))...)
	return out, nil
}

func (s *Server) expeditionPartyLeader(ctx context.Context, repo domain.PartyRepository, me domain.CharacterSession, e *expedition, target int64) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	p, ok := e.partyOf(target)
	_, leaders := p.Member(e.leader)
	b.mu.Unlock()
	// The expedition leader's own party keeps its leader (0x44 moves both).
	if !ok || p.LeaderID == target || leaders {
		return nil, nil
	}
	result, out, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: me, Action: domain.PartySetLeader, PartyID: p.ID, TargetID: target})
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out = append(out, b.expeditionPartyChanged(e, result.Before, result.After)...)
	return out, nil
}

// expeditionMove moves an online member to party index, or into a new party
// when index is the next free one.
func (s *Server) expeditionMove(ctx context.Context, repo domain.PartyRepository, e *expedition, target int64, index int) ([]partyDelivery, error) {
	b := s.parties
	b.mu.Lock()
	from, ok := e.partyOf(target)
	peer := b.peers[target]
	t, okType := expeditionType(e.kind)
	valid := ok && okType && peer != nil && peer.party.ID == from.ID && index <= len(e.parties) && index < t.MaxParties()
	var dest int64
	if valid && index < len(e.parties) {
		dest = e.parties[index]
		valid = dest != from.ID && len(e.snapshots[dest].Members) < domain.PartyCapacity
	}
	var who domain.CharacterSession
	if valid {
		who = partySession(peer.live)
		who.Lease = s.currentLease()
	}
	b.mu.Unlock()
	if !valid {
		return nil, nil
	}
	removed, out, err := s.commitParty(ctx, repo, domain.PartyRequest{Session: who, Action: domain.PartyRemove, PartyID: from.ID, TargetID: target})
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	out = append(out, b.expeditionPartyChanged(e, removed.Before, removed.After)...)
	b.mu.Unlock()
	var joined domain.PartyMutation
	var more []partyDelivery
	if dest != 0 {
		joined, more, err = s.commitParty(ctx, repo, domain.PartyRequest{Session: who, Action: domain.PartyJoin, PartyID: dest})
	} else {
		joined, more, err = s.commitParty(ctx, repo, domain.PartyRequest{Session: who, Action: domain.PartyCreate})
	}
	out = append(out, more...)
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if dest == 0 {
		b.addExpeditionParty(e, joined.After)
		out = append(out, b.expeditionBroadcast(e, 0, protocol.ExpeditionPartyUpdate(e.leaderIndex(), e.index(joined.After.ID), joined.After))...)
	} else {
		out = append(out, b.expeditionPartyChanged(e, joined.Before, joined.After)...)
	}
	// The moved member missed the update for its old party while it was
	// between parties; give it the whole expedition again.
	out = append(out, partyDelivery{peer: peer, packets: [][]byte{protocol.ExpeditionStatus(e.kind, e.leaderIndex(), b.expeditionParties(e), false, false)}})
	return out, nil
}

// expeditionAfterParty keeps an expedition in step with an ordinary party
// command on one of its parties (the party tab's own leave, expel or leader
// buttons). The caller holds b.mu.
func (b *partySpace) expeditionAfterParty(q domain.PartyRequest, result domain.PartyMutation) []partyDelivery {
	id := result.Before.ID
	if id == 0 {
		id = result.After.ID
	}
	e := b.exp.byParty[id]
	if e == nil {
		return nil
	}
	var out []partyDelivery
	if q.Action == domain.PartyLeave || q.Action == domain.PartyExpel {
		for _, m := range result.Before.Members {
			if _, still := result.After.Member(m.CharacterID); still {
				continue
			}
			if p := b.peers[m.CharacterID]; p != nil {
				reason := "left"
				if q.Action == domain.PartyExpel && m.CharacterID == result.TargetID {
					reason = "kicked"
				}
				out = append(out, partyDelivery{peer: p, packets: [][]byte{protocol.ExpeditionRemoved(reason)}})
			}
		}
	}
	out = append(out, b.expeditionPartyChanged(e, result.Before, result.After)...)
	if len(e.parties) == 0 {
		for tid, inv := range b.exp.invites {
			if inv.exp == e {
				delete(b.exp.invites, tid)
			}
		}
	}
	return out
}

// expeditionPresence refreshes one party of an expedition after a presence
// change (map, channel, login, logout). The caller holds b.mu.
func (b *partySpace) expeditionPresence(p domain.PartySnapshot) []partyDelivery {
	e := b.exp.byParty[p.ID]
	if e == nil {
		return nil
	}
	i := e.index(p.ID)
	return b.expeditionBroadcast(e, 0, protocol.ExpeditionPartyUpdate(e.leaderIndex(), i, b.visible(e.snapshots[p.ID])))
}

// expeditionEnter sends the whole expedition to a member entering the field.
// The caller holds b.mu.
func (b *partySpace) expeditionEnter(peer *partyPeer) []partyDelivery {
	e := b.exp.byParty[peer.party.ID]
	if e == nil {
		return nil
	}
	return []partyDelivery{{peer: peer, packets: [][]byte{protocol.ExpeditionStatus(e.kind, e.leaderIndex(), b.expeditionParties(e), false, false)}}}
}
