package world

import (
	"context"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// guildScriptCommand serves the guild office and alliance NPC scripts. Every
// mutating branch goes through the same durable command as GUILD_OPERATION.
func (s *Server) guildScriptCommand(ctx context.Context, live *liveCharacter, sink packetSink, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	value := func(v any) (scripting.CommandResult, error) { return scripting.CommandResult{Value: v}, nil }
	switch command.Kind {
	case "guild_rank":
		return value(int64(snapshot.Character.Guild.Rank))
	case "guild_prompt":
		// 1 asks for a new guild name, 18 opens the emblem editor.
		if len(command.Ints) != 1 || command.Ints[0] != 1 && command.Ints[0] != 18 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		return scripting.CommandResult{}, sink.WritePacket(protocol.GuildMessage(byte(command.Ints[0])))
	case "guild_disband":
		ok, err := s.commitGuild(ctx, live, sink, domain.GuildRequest{Action: domain.GuildDisband})
		return scripting.CommandResult{Value: ok}, err
	case "guild_capacity_limit":
		// 0: how far mesos expansion goes, 1: how far GP expansion goes.
		if len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] > 1 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		if command.Ints[0] == 1 {
			return value(int64(domain.GuildCapacityLimit()))
		}
		return value(int64(domain.GuildMesosCapacityLimit()))
	case "guild_capacity":
		if len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] > 1 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		ok, err := s.commitGuild(ctx, live, sink, domain.GuildRequest{Action: domain.GuildCapacity, UseGP: command.Ints[0] == 1})
		return scripting.CommandResult{Value: ok}, err
	case "alliance_rank":
		repo, ok := s.repo.(domain.GuildRepository)
		if !ok || snapshot.Character.Guild.ID == 0 {
			return value(int64(0))
		}
		g, err := repo.ReadGuild(ctx, snapshot)
		if err != nil {
			return scripting.CommandResult{}, err
		}
		if m, ok := g.Member(snapshot.CharacterID); ok && g.Alliance != 0 {
			return value(int64(m.AllianceRank))
		}
		return value(int64(0))
	case "alliance_create":
		partner, ok := s.alliancePartner(snapshot)
		if !ok {
			return value(false)
		}
		committed, err := s.commitAlliance(ctx, live, sink, domain.AllianceRequest{Action: domain.AllianceCreate, Name: command.Text, PartnerID: partner})
		return scripting.CommandResult{Value: committed}, err
	case "alliance_capacity":
		ok, err := s.commitAlliance(ctx, live, sink, domain.AllianceRequest{Action: domain.AllianceCapacity})
		return scripting.CommandResult{Value: ok}, err
	case "alliance_disband":
		ok, err := s.commitAlliance(ctx, live, sink, domain.AllianceRequest{Action: domain.AllianceDisband})
		return scripting.CommandResult{Value: ok}, err
	}
	return scripting.CommandResult{}, domain.ErrInvalidInput
}

// alliancePartner is Legacy's createAlliance precondition: a party of exactly
// two, led by the caller, with the partner online in the same field.
func (s *Server) alliancePartner(snapshot domain.CharacterSession) (int64, bool) {
	if s.parties == nil {
		return 0, false
	}
	s.parties.mu.Lock()
	peer := s.parties.peers[snapshot.CharacterID]
	if peer == nil || peer.party.LeaderID != snapshot.CharacterID || len(peer.party.Members) != 2 {
		s.parties.mu.Unlock()
		return 0, false
	}
	var other *partyPeer
	for _, m := range peer.party.Members {
		if m.CharacterID != snapshot.CharacterID {
			other = s.parties.peers[m.CharacterID]
		}
	}
	s.parties.mu.Unlock()
	if other == nil {
		return 0, false
	}
	other.live.mu.Lock()
	o := other.live.session
	other.live.mu.Unlock()
	if o.ChannelID != snapshot.ChannelID || o.Character.MapID != snapshot.Character.MapID {
		return 0, false
	}
	return o.CharacterID, true
}
