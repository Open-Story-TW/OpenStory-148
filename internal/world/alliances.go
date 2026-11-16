package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// Alliances reuse guild presence: delivery is to online members of every
// allied guild. Lock order matches guilds: scene gate -> guild commands.

func (s *Server) enterAlliance(ctx context.Context, live *liveCharacter, sink packetSink, space *guildSpace, g domain.GuildSnapshot) error {
	repo, ok := s.repo.(domain.AllianceRepository)
	if !ok || g.Alliance == 0 {
		return nil
	}
	call, done := context.WithTimeout(ctx, 3*time.Second)
	a, err := repo.ReadAlliance(call, s.petSession(live))
	done()
	if err != nil || a.ID == 0 {
		return err
	}
	for _, packet := range append([][]byte{protocol.AllianceInfo(a)}, protocol.AllianceGuilds(a, space.online)...) {
		if err = sink.WritePacket(packet); err != nil {
			return err
		}
	}
	self := s.petSession(live).CharacterID
	space.deliver(a.Members(), self, protocol.AllianceMemberOnline(a.ID, g.ID, self, true))
	return nil
}

func (s *Server) handleAlliance(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadAlliance(r)
	if err != nil {
		return err
	}
	if req.Action == "info" {
		repo, ok := s.repo.(domain.AllianceRepository)
		if !ok {
			return nil
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		a, err := repo.ReadAlliance(call, s.petSession(live))
		done()
		if err != nil {
			return petFailure(sink, err)
		}
		if err = sink.WritePacket(protocol.AllianceInfo(a)); err != nil || a.ID == 0 {
			return err
		}
		for _, packet := range protocol.AllianceGuilds(a, s.guildSpace().online) {
			if err = sink.WritePacket(packet); err != nil {
				return err
			}
		}
		return nil
	}
	return s.mutateAlliance(ctx, live, sink, req)
}

func allianceFailure(err error) []byte {
	switch {
	case errors.Is(err, domain.ErrAlreadyExists):
		return protocol.Notice("這個名字不能使用，請嘗試其他的。")
	case errors.Is(err, domain.ErrGuildFull):
		return protocol.Notice("公會聯盟容量已經滿了。")
	case errors.Is(err, domain.ErrNotFound):
		return protocol.Notice("沒有找到那個公會。")
	case errors.Is(err, domain.ErrInsufficient):
		return protocol.Notice("你沒有足夠的楓幣。")
	}
	return protocol.EnableActions()
}

func (s *Server) mutateAlliance(ctx context.Context, live *liveCharacter, sink packetSink, req domain.AllianceRequest) error {
	_, err := s.commitAlliance(ctx, live, sink, req)
	return err
}

// commitAlliance persists and publishes one alliance command. The bool
// reports whether it committed, for NPC scripts that branch on success.
func (s *Server) commitAlliance(ctx context.Context, live *liveCharacter, sink packetSink, req domain.AllianceRequest) (bool, error) {
	repo, ok := s.repo.(domain.AllianceRepository)
	if !ok {
		return false, sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	req.Session = s.petSession(live)
	req.OperationID = "alliance-" + string(req.Action) + "-" + platform.NewID()
	call, done := context.WithTimeout(ctx, 3*time.Second)
	m, err := repo.MutateAlliance(call, req)
	done()
	if err != nil {
		if errors.Is(err, domain.ErrStaleOwner) {
			return false, err
		}
		return false, sink.WritePacket(allianceFailure(err))
	}
	space := s.guildSpace()
	space.commands.Lock()
	defer space.commands.Unlock()
	if m.Character != nil {
		live.mu.Lock()
		live.session.Character.Mesos = m.Character.Mesos
		live.session.Character.Revision = m.Character.Revision
		live.mu.Unlock()
		if err = sink.WritePacket(protocol.UpdateMesos(m.Character.Mesos, true)); err != nil {
			return true, err
		}
	}
	a, before := m.After, m.Before
	online := space.online
	switch m.Action {
	case domain.AllianceCreate:
		packets := append([][]byte{protocol.AllianceCreated(a, online), protocol.AllianceInfo(a)}, protocol.AllianceGuilds(a, online)...)
		space.deliver(a.Members(), 0, append(packets, protocol.AllianceMembership(a, true))...)
	case domain.AllianceInvite:
		if p, ok := space.peer(m.InviteeID); ok {
			_ = p.sink.WritePacket(protocol.AllianceInvite(req.Session.Character.Guild.ID, req.Session.Character.Name, before.Name))
		}
	case domain.AllianceDeny:
		if p, ok := space.peer(m.InviteeID); ok {
			_ = p.sink.WritePacket(protocol.Notice(fmt.Sprintf("%s 公會拒絕了公會的邀請.", req.Session.Character.Guild.Name)))
		}
	case domain.AllianceAccept:
		space.deliver(a.Members(), 0, protocol.AllianceGuildJoined(a, m.Guild, online), protocol.AllianceMembership(a, true))
		space.deliver(m.Guild.Members, 0, append([][]byte{protocol.AllianceInfo(a)}, protocol.AllianceGuilds(a, online)...)...)
	case domain.AllianceLeave, domain.AllianceExpel:
		space.deliver(a.Members(), 0, protocol.AllianceGuildLeft(a, m.Guild, m.Expelled, online))
		// Legacy removeGuild: the departing guild only gets disbandAlliance
		// (op 29); a v145 client drops the connection on op 16 for its own guild.
		space.deliver(m.Guild.Members, 0, protocol.AllianceDisbanded(a.ID))
	case domain.AllianceLeader:
		space.deliver(a.Members(), 0, protocol.AllianceLeaderChanged(a.ID, req.Session.CharacterID, req.TargetID))
	case domain.AllianceTitles:
		space.deliver(a.Members(), 0, protocol.AllianceTitlesChanged(a.ID, a.Titles))
	case domain.AllianceRank:
		space.deliver(a.Members(), 0, protocol.AllianceRankChanged(req.TargetID, m.Rank))
	case domain.AllianceNotice:
		space.deliver(a.Members(), 0, protocol.AllianceNoticeChanged(a.ID, a.Notice))
	case domain.AllianceCapacity:
		space.deliver(a.Members(), 0, protocol.AllianceUpdate(a))
	case domain.AllianceDisband:
		space.deliver(before.Members(), 0, protocol.AllianceDisbanded(before.ID))
	}
	return true, nil
}

// handleMultiChat is Legacy ChatHandler.Others for party, guild and alliance.
// Recipients are always derived from durable membership, never the packet.
func (s *Server) handleMultiChat(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadMultiChat(r)
	if err != nil {
		return err
	}
	live.mu.Lock()
	session := live.session
	// ReadAlliance checks ownership against the world lease.
	session.Lease = s.currentLease()
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
	if session.Character.MapID == oxQuizMap && s.oxQuizInProgress(session.ChannelID) {
		return nil
	}
	if kind, known := map[int]string{0: "chat_buddy", 1: "chat_party", 2: "chat_guild", 3: "chat_alliance"}[req.Type]; known {
		recordChat(kind, session, req.Text)
	}
	packet := protocol.MultiChatText(req.Type, session.Character.Name, req.Text)
	space := s.guildSpace()
	var members []domain.GuildMemberInfo
	switch req.Type {
	case 1:
		if s.parties == nil {
			return nil
		}
		s.parties.mu.Lock()
		var sinks []packetSink
		if peer := s.parties.peers[session.CharacterID]; peer != nil {
			for _, m := range peer.party.Members {
				if other := s.parties.peers[m.CharacterID]; other != nil && m.CharacterID != session.CharacterID {
					sinks = append(sinks, other.sink)
				}
			}
		}
		s.parties.mu.Unlock()
		for _, target := range sinks {
			_ = target.WritePacket(packet)
		}
		return nil
	case 2:
		repo, ok := s.repo.(domain.GuildRepository)
		if !ok || session.Character.Guild.ID == 0 {
			return nil
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		g, err := repo.ReadGuild(call, session)
		done()
		if err != nil {
			return petFailure(sink, err)
		}
		members = g.Members
	case 3:
		repo, ok := s.repo.(domain.AllianceRepository)
		if !ok || session.Character.Guild.ID == 0 {
			return nil
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		a, err := repo.ReadAlliance(call, session)
		done()
		if err != nil {
			return petFailure(sink, err)
		}
		members = a.Members()
	default:
		return nil
	}
	space.deliver(members, session.CharacterID, packet)
	return nil
}

// tickGuild grants Legacy's hourly presence GP (doFairy: +20 and contribution).
func (s *Server) tickGuild(ctx context.Context, live *liveCharacter, sink packetSink, now time.Time) {
	live.mu.Lock()
	inGuild := live.session.Character.Guild.ID != 0
	if !inGuild {
		live.guildGPAt = time.Time{}
		live.mu.Unlock()
		return
	}
	if live.guildGPAt.IsZero() {
		live.guildGPAt = now.Add(time.Hour)
	}
	due := !now.Before(live.guildGPAt)
	if due {
		live.guildGPAt = now.Add(time.Hour)
	}
	session := live.session
	live.mu.Unlock()
	if !due {
		return
	}
	period := fmt.Sprintf("hour-%s-%d", session.SessionID, now.Unix()/3600)
	_ = s.mutateGuild(ctx, live, sink, domain.GuildRequest{Action: domain.GuildGainGP, Amount: domain.GuildHourlyGP, Period: period})
}
