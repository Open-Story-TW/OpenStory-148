package world

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// guildSpace is world-local presence for guild delivery. Membership itself
// is durable in PostgreSQL; this registry only answers "who is online here".
type guildSpace struct {
	commands sync.Mutex // orders one world's guild commits and online state changes
	mu       sync.Mutex
	peers    map[int64]guildPeer
}

type guildPeer struct {
	live *liveCharacter
	sink packetSink
}

func (s *Server) guildSpace() *guildSpace {
	s.guildOnce.Do(func() { s.guilds = &guildSpace{peers: map[int64]guildPeer{}} })
	return s.guilds
}

func (g *guildSpace) peer(id int64) (guildPeer, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.peers[id]
	return p, ok
}

func (g *guildSpace) online(id int64) bool {
	_, ok := g.peer(id)
	return ok
}

// deliver writes packets to every online member except skip.
func (g *guildSpace) deliver(members []domain.GuildMemberInfo, skip int64, packets ...[]byte) {
	for _, m := range members {
		if m.CharacterID == skip {
			continue
		}
		if p, ok := g.peer(m.CharacterID); ok {
			for _, packet := range packets {
				if packet != nil {
					_ = p.sink.WritePacket(packet)
				}
			}
		}
	}
}

func (s *Server) enterGuild(ctx context.Context, live *liveCharacter, sink packetSink) error {
	repo, ok := s.repo.(domain.GuildRepository)
	if !ok {
		return nil
	}
	session := s.petSession(live)
	space := s.guildSpace()
	space.commands.Lock()
	defer space.commands.Unlock()
	call, done := context.WithTimeout(ctx, 3*time.Second)
	g, err := repo.ReadGuild(call, session)
	done()
	if err != nil {
		return err
	}
	live.mu.Lock()
	live.session.Character.Guild = g.Tag(session.CharacterID)
	live.session.Character.GuildSkills = g.Skills
	live.mu.Unlock()
	space.mu.Lock()
	space.peers[session.CharacterID] = guildPeer{live: live, sink: sink}
	space.mu.Unlock()
	if g.ID == 0 {
		return nil
	}
	if err = sink.WritePacket(protocol.GuildInfo(g, space.online)); err != nil {
		return err
	}
	space.deliver(g.Members, session.CharacterID, protocol.GuildMemberOnline(g.ID, session.CharacterID, true))
	return s.enterAlliance(ctx, live, sink, space, g)
}

func (s *Server) leaveGuild(live *liveCharacter) {
	live.mu.Lock()
	id, tag := live.session.CharacterID, live.session.Character.Guild
	live.mu.Unlock()
	space := s.guildSpace()
	space.commands.Lock()
	defer space.commands.Unlock()
	space.mu.Lock()
	if p, ok := space.peers[id]; !ok || p.live != live {
		space.mu.Unlock()
		return
	}
	delete(space.peers, id)
	space.mu.Unlock()
	repo, ok := s.repo.(domain.GuildRepository)
	if !ok || tag.ID == 0 {
		return
	}
	// Presence only: a stale read just skips the offline notice.
	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	g, err := repo.ReadGuild(ctx, s.petSession(live))
	done()
	if err == nil && g.ID != 0 {
		space.deliver(g.Members, id, protocol.GuildMemberOnline(g.ID, id, false))
	}
}

func guildFailure(err error) []byte {
	switch {
	case errors.Is(err, domain.ErrAlreadyExists):
		return protocol.GuildMessage(protocol.GuildMsgNameTaken)
	case errors.Is(err, domain.ErrGuildFull):
		return protocol.GuildMessage(protocol.GuildMsgFull)
	case errors.Is(err, domain.ErrNotFound):
		return protocol.GuildMessage(protocol.GuildMsgNotFound)
	case errors.Is(err, domain.ErrGuildInvitePending):
		return protocol.GuildMessage(protocol.GuildMsgBusy)
	case errors.Is(err, domain.ErrInsufficient):
		return protocol.Notice("你沒有足夠的楓幣。")
	}
	return protocol.EnableActions()
}

func (s *Server) handleGuildDeny(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	reason, inviter, err := protocol.ReadGuildDeny(r)
	if err != nil {
		return err
	}
	slog.Info("guild_invite_denied", "character", s.petSession(live).CharacterID, "inviter", inviter, "reason", reason)
	return s.mutateGuild(ctx, live, sink, domain.GuildRequest{Action: domain.GuildDeny})
}

func (s *Server) handleGuild(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadGuild(r)
	if err != nil {
		return err
	}
	session := s.petSession(live)
	switch req.Action {
	case "info":
		repo, ok := s.repo.(domain.GuildRepository)
		if !ok {
			return nil
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		g, err := repo.ReadGuild(call, session)
		done()
		if err != nil {
			return petFailure(sink, err)
		}
		return sink.WritePacket(protocol.GuildInfo(g, s.guildSpace().online))
	case domain.GuildAccept:
		if req.TargetID != session.CharacterID {
			return nil
		}
	case domain.GuildLeave:
		if req.TargetID != session.CharacterID || req.Name != session.Character.Name {
			return nil
		}
	case domain.GuildCreate:
		if session.Character.Guild.ID != 0 {
			return sink.WritePacket(protocol.Notice("無法建立公會，已經有公會。"))
		}
		if session.Character.Mesos < domain.GuildCreateCost {
			return sink.WritePacket(protocol.Notice("你沒有足夠的楓幣建立公會。"))
		}
	case domain.GuildInvite:
		if !s.guildTargetOnline(req.Name) {
			return sink.WritePacket(protocol.GuildMessage(protocol.GuildMsgNotFound))
		}
	}
	return s.mutateGuild(ctx, live, sink, req)
}

func (s *Server) guildTargetOnline(name string) bool {
	space := s.guildSpace()
	space.mu.Lock()
	defer space.mu.Unlock()
	for _, p := range space.peers {
		p.live.mu.Lock()
		match := equalFoldName(p.live.session.Character.Name, name)
		p.live.mu.Unlock()
		if match {
			return true
		}
	}
	return false
}

func equalFoldName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func (s *Server) mutateGuild(ctx context.Context, live *liveCharacter, sink packetSink, req domain.GuildRequest) error {
	_, err := s.commitGuild(ctx, live, sink, req)
	return err
}

// commitGuild persists and publishes one guild command; the bool reports a
// commit so NPC scripts can branch on it.
func (s *Server) commitGuild(ctx context.Context, live *liveCharacter, sink packetSink, req domain.GuildRequest) (bool, error) {
	repo, ok := s.repo.(domain.GuildRepository)
	if !ok {
		return false, sink.WritePacket(protocol.EnableActions())
	}
	// Fees debit the meso ledger, so they serialize with other asset commands.
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	req.Session = s.petSession(live)
	req.OperationID = "guild-" + string(req.Action) + "-" + platform.NewID()
	space := s.guildSpace()
	space.commands.Lock()
	var updates []guildTagUpdate
	defer func() {
		// Field entry takes its scene gate before the guild command lock.
		space.commands.Unlock()
		s.publishGuildTags(updates)
	}()
	call, done := context.WithTimeout(ctx, 3*time.Second)
	m, err := repo.MutateGuild(call, req)
	done()
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return false, err
		}
		if req.Action == domain.GuildGainGP {
			return false, nil // presence GP is silent when already granted
		}
		return false, sink.WritePacket(guildFailure(err))
	}
	self := req.Session.CharacterID
	if m.Character != nil {
		live.mu.Lock()
		live.session.Character.Mesos = m.Character.Mesos
		live.session.Character.Revision = m.Character.Revision
		live.mu.Unlock()
	}
	g := m.After
	before := m.Before
	updates = installGuildState(space, before, g)
	if m.Character != nil {
		if err = sink.WritePacket(protocol.UpdateMesos(m.Character.Mesos, true)); err != nil {
			return true, err
		}
	}
	switch m.Action {
	case domain.GuildCreate:
		err = sink.WritePacket(protocol.GuildCreated(g, space.online))
	case domain.GuildInvite:
		if p, ok := space.peer(m.TargetID); ok && m.Invitation != nil {
			_ = p.sink.WritePacket(protocol.GuildInviteNotice(*m.Invitation))
		}
	case domain.GuildAccept:
		if joined, ok := g.Member(self); ok {
			space.deliver(g.Members, self, protocol.GuildMemberJoined(g.ID, joined, true))
		}
		err = sink.WritePacket(protocol.GuildInfo(g, space.online))
	case domain.GuildDeny:
		if m.Invitation != nil {
			if p, ok := space.peer(m.Invitation.InviterID); ok {
				_ = p.sink.WritePacket(protocol.GuildDenied(req.Session.Character.Name))
			}
		}
	case domain.GuildLeave:
		if m.Disbanded {
			err = sink.WritePacket(protocol.GuildDisbanded(before.ID))
		} else {
			space.deliver(before.Members, self, protocol.GuildMemberLeft(before.ID, self, m.TargetName, false))
		}
		if err == nil {
			err = sink.WritePacket(protocol.GuildInfo(domain.GuildSnapshot{}, nil))
		}
	case domain.GuildExpel:
		space.deliver(before.Members, 0, protocol.GuildMemberLeft(before.ID, m.TargetID, m.TargetName, true))
		if p, ok := space.peer(m.TargetID); ok {
			_ = p.sink.WritePacket(protocol.GuildInfo(domain.GuildSnapshot{}, nil))
		}
	case domain.GuildChangeRank:
		if target, ok := g.Member(m.TargetID); ok {
			space.deliver(g.Members, 0, protocol.GuildRankChanged(g.ID, target.CharacterID, target.Rank))
		}
	case domain.GuildTitles:
		space.deliver(g.Members, 0, protocol.GuildTitlesChanged(g.ID, g.Titles))
	case domain.GuildEmblem:
		space.deliver(g.Members, 0, protocol.GuildEmblemChanged(g.ID, g.Emblem))
	case domain.GuildNotice:
		space.deliver(g.Members, 0, protocol.GuildNoticeChanged(g.ID, g.Notice))
	case domain.GuildLeader:
		space.deliver(g.Members, 0, protocol.GuildLeaderChanged(g.ID, self, m.TargetID))
	case domain.GuildDisband:
		space.deliver(before.Members, 0, protocol.GuildDisbanded(before.ID), protocol.GuildInfo(domain.GuildSnapshot{}, nil))
	case domain.GuildCapacity:
		space.deliver(g.Members, 0, protocol.GuildCapacityChanged(g.ID, g.Capacity))
	case domain.GuildSkillBuy, domain.GuildSkillRenew:
		if m.Skill != nil {
			space.deliver(g.Members, 0, protocol.GuildSkillChanged(g.ID, *m.Skill), protocol.GuildGP(g.ID, g.GP))
		}
	case domain.GuildGainGP:
		if joined, ok := g.Member(self); ok {
			space.deliver(g.Members, 0, protocol.GuildContribution(g.ID, self, joined.Contribution), protocol.GuildGP(g.ID, g.GP))
		}
		err = sink.WritePacket(protocol.GPGainMessage(req.Amount, true))
	}
	return true, err
}

type guildTagUpdate struct {
	peer guildPeer
	tag  domain.GuildTag
}

// installGuildState runs under commands so a successful purchase updates all
// online members before the native skill notification is delivered.
func installGuildState(space *guildSpace, before, after domain.GuildSnapshot) []guildTagUpdate {
	ids := map[int64]bool{}
	for _, m := range before.Members {
		ids[m.CharacterID] = true
	}
	for _, m := range after.Members {
		ids[m.CharacterID] = true
	}
	var updates []guildTagUpdate
	for id := range ids {
		p, ok := space.peer(id)
		if !ok {
			continue
		}
		tag := after.Tag(id)
		p.live.mu.Lock()
		changed := p.live.session.Character.Guild != tag
		p.live.session.Character.Guild = tag
		p.live.session.Character.GuildSkills = nil
		if tag.ID != 0 {
			p.live.session.Character.GuildSkills = after.Skills
		}
		p.live.mu.Unlock()
		if changed {
			updates = append(updates, guildTagUpdate{peer: p, tag: tag})
		}
	}
	return updates
}

func (s *Server) publishGuildTags(updates []guildTagUpdate) {
	for _, update := range updates {
		p, tag := update.peer, update.tag
		snapshot := s.petSession(p.live)
		if snapshot.Character.Guild != tag {
			continue
		}
		s.updateOnlineOwned(snapshot, snapshot)
		v := s.sceneFor(p.live, p.sink)
		if v == nil || s.maps == nil {
			continue
		}
		id := snapshot.CharacterID
		unlock, err := s.lockScenePublication(id)
		if err != nil {
			continue
		}
		if s.sceneCurrent(v) && s.petSession(p.live).Character.Guild == tag {
			s.maps.publishGuildTag(v, tag, protocol.LoadGuildName(id, tag), protocol.LoadGuildIcon(id, tag))
		}
		unlock()
	}
}

func (m *mapSpace) publishGuildTag(v *sceneVisit, tag domain.GuildTag, packets ...[]byte) {
	m.mu.Lock()
	owner := v.owner
	self := m.rooms[mapKey{channel: owner.ChannelID, mapID: owner.Character.MapID}][owner.CharacterID]
	if self != nil && sameDropSink(self.sink, v.sink) {
		self.look.Guild = tag
	}
	m.mu.Unlock()
	m.publishPets(v, domain.Character{}, true, packets...)
}
