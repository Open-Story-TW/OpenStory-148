package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const (
	carnivalLegacyMinLevel = 51
	carnivalLegacyMaxLevel = 70
	carnivalLegacyWait     = 3 * time.Minute
)

type carnivalLegacyRoster struct {
	partyID int64
	leader  int64
	members []*carnivalPlayer
}

type carnivalLegacyQueue struct {
	key      mapKey
	first    *carnivalLegacyRoster
	second   *carnivalLegacyRoster
	deadline time.Time
	ready    bool
}

func (s *Server) carnivalLegacyEnabled() bool {
	return s.gameplay != nil && s.gameplay.carnival != nil && s.gameplay.carnival.legacy != nil
}

func carnivalLegacyArena(slot int) (arena, minSize, maxSize int) {
	switch slot {
	case 1:
		return 980031100, 2, 4
	case 2:
		return 980032100, 2, 4
	case 3:
		return 980033100, 3, 6
	default:
		return 0, 0, 0
	}
}

func (s *Server) enterCarnivalLegacy(ctx context.Context, a *worldScript, snapshot domain.CharacterSession) string {
	c := snapshot.Character
	if !s.carnivalLegacyEnabled() || a.npcID != 9010022 || c.Level < carnivalLegacyMinLevel ||
		c.Level > carnivalLegacyMaxLevel || c.HP <= 0 || s.eventOf(c.ID) != nil ||
		content.CarnivalMatchField(c.MapID) || content.CarnivalSecondField(c.MapID) {
		return "unavailable"
	}
	visit := s.sceneFor(a.live, a.sink)
	if visit == nil {
		return "unavailable"
	}
	g := s.gameplay
	g.mu.Lock()
	if g.carnival.entrants[c.ID] != nil {
		g.mu.Unlock()
		return "unavailable"
	}
	p := &carnivalPlayer{owner: visit, home: c.MapID, waitMap: content.CarnivalSecondLobby, legacy: true}
	g.carnival.entrants[c.ID] = p
	g.mu.Unlock()
	if s.carnivalWarp(ctx, p, c.MapID, content.CarnivalSecondLobby, "st00") {
		return "entered"
	}
	g.mu.Lock()
	if g.carnival.entrants[c.ID] == p {
		delete(g.carnival.entrants, c.ID)
	}
	g.mu.Unlock()
	return "unavailable"
}

func (s *Server) carnivalLegacyRoster(leader domain.CharacterSession, minSize, maxSize int) (*carnivalLegacyRoster, string) {
	if s.parties == nil {
		return nil, "party"
	}
	s.parties.mu.Lock()
	peer := s.parties.peers[leader.CharacterID]
	if peer == nil || peer.party.ID <= 0 {
		s.parties.mu.Unlock()
		return nil, "party"
	}
	party := peer.party
	if party.LeaderID != leader.CharacterID {
		s.parties.mu.Unlock()
		return nil, "leader"
	}
	if n := len(party.Members); n < minSize || n > maxSize {
		s.parties.mu.Unlock()
		return nil, "size"
	}
	peers := make([]*partyPeer, 0, len(party.Members))
	for _, member := range party.Members {
		p := s.parties.peers[member.CharacterID]
		if p == nil || p.party.ID != party.ID {
			s.parties.mu.Unlock()
			return nil, "here"
		}
		peers = append(peers, p)
	}
	s.parties.mu.Unlock()
	visits := make([]*sceneVisit, 0, len(peers))
	for _, peer := range peers {
		v := s.sceneFor(peer.live, peer.sink)
		p := partySession(peer.live)
		if v == nil || p.ChannelID != leader.ChannelID || p.Character.MapID != content.CarnivalSecondLobby ||
			p.Character.HP <= 0 || p.Character.Level < carnivalLegacyMinLevel || p.Character.Level > carnivalLegacyMaxLevel ||
			s.tradeActive(peer.live) {
			return nil, "here"
		}
		visits = append(visits, v)
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	roster := &carnivalLegacyRoster{partyID: party.ID, leader: leader.CharacterID}
	for _, v := range visits {
		p := g.carnival.entrants[v.owner.CharacterID]
		if p == nil || !p.legacy || p.match != nil || p.legacyQueue != nil ||
			!sameSceneOwner(p.owner.owner, v.owner) {
			return nil, "here"
		}
		roster.members = append(roster.members, p)
	}
	return roster, ""
}

func (s *Server) registerCarnivalLegacy(ctx context.Context, a *worldScript, leader domain.CharacterSession, slot int) string {
	if !s.carnivalLegacyEnabled() || a.npcID != 2042005 || leader.Character.MapID != content.CarnivalSecondLobby {
		return "unavailable"
	}
	arena, minSize, maxSize := carnivalLegacyArena(slot)
	if arena == 0 {
		return "unavailable"
	}
	roster, reason := s.carnivalLegacyRoster(leader, minSize, maxSize)
	if reason != "" {
		return reason
	}
	g := s.gameplay
	key := mapKey{channel: leader.ChannelID, mapID: arena}
	g.mu.Lock()
	for _, p := range roster.members {
		if g.carnival.entrants[p.owner.owner.CharacterID] != p || p.match != nil || p.legacyQueue != nil {
			g.mu.Unlock()
			return "unavailable"
		}
	}
	if r := g.rooms[key]; r != nil && r.carnival != nil {
		g.mu.Unlock()
		return "busy"
	}
	queue := g.carnival.legacy[key]
	if queue != nil {
		if queue.first.partyID == roster.partyID || !queue.ready || queue.second != nil || !time.Now().Before(queue.deadline) {
			g.mu.Unlock()
			return "busy"
		}
		if len(queue.first.members) != len(roster.members) {
			g.mu.Unlock()
			return "equal"
		}
		queue.second = roster
		for _, p := range roster.members {
			p.legacyQueue = queue
		}
		host := g.carnival.entrants[queue.first.leader]
		g.mu.Unlock()
		if host != nil {
			if visit := s.sceneFor(host.owner.live, host.owner.sink); visit != nil {
				_ = s.writeScenePackets(visit, protocol.Notice("另一支隊伍向你們提出挑戰。請由隊長與休菲凱曼對話。"))
			}
		}
		return "challenged"
	}
	room, err := g.room(key)
	if err != nil || room.definition.Carnival == nil || len(room.members) != 0 {
		g.mu.Unlock()
		return "unavailable"
	}
	queue = &carnivalLegacyQueue{key: key, first: roster, deadline: time.Now().Add(carnivalLegacyWait)}
	g.carnival.legacy[key] = queue
	for _, p := range roster.members {
		p.waitMap, p.legacyQueue = arena-100, queue
	}
	g.mu.Unlock()
	for _, p := range roster.members {
		if !s.carnivalWarp(ctx, p, content.CarnivalSecondLobby, arena-100, "") {
			s.resetCarnivalLegacyQueue(ctx, queue)
			return "here"
		}
	}
	g.mu.Lock()
	queue.ready = g.carnival.legacy[key] == queue
	ready := queue.ready
	g.mu.Unlock()
	if !ready {
		return "unavailable"
	}
	return "registered"
}

func (s *Server) acceptCarnivalLegacy(ctx context.Context, snapshot domain.CharacterSession) string {
	g := s.gameplay
	cid := snapshot.CharacterID
	g.mu.Lock()
	p := g.carnival.entrants[cid]
	if p == nil || p.legacyQueue == nil || !sameSceneOwner(p.owner.owner, snapshot) {
		g.mu.Unlock()
		return "unavailable"
	}
	q := p.legacyQueue
	if g.carnival.legacy[q.key] != q || q.first.leader != cid || snapshot.Character.MapID != q.key.mapID-100 ||
		!q.ready || q.second == nil || !time.Now().Before(q.deadline) {
		g.mu.Unlock()
		return "pending"
	}
	teams := []*carnivalLegacyRoster{q.first, q.second}
	g.mu.Unlock()
	for i, roster := range teams {
		if !s.carnivalLegacyPartyCurrent(roster) {
			s.resetCarnivalLegacyQueue(ctx, q)
			return "here"
		}
		mapID := q.key.mapID - 100
		if i == 1 {
			mapID = content.CarnivalSecondLobby
		}
		for _, player := range roster.members {
			v := s.sceneFor(player.owner.live, player.owner.sink)
			current := partySession(player.owner.live)
			if v == nil || current.Character.MapID != mapID || current.Character.HP <= 0 ||
				s.partyOf(current.CharacterID) != roster.partyID {
				s.resetCarnivalLegacyQueue(ctx, q)
				return "here"
			}
		}
	}
	g.mu.Lock()
	room := g.rooms[q.key]
	if g.carnival.legacy[q.key] != q || !time.Now().Before(q.deadline) ||
		room == nil || room.carnival != nil || len(room.members) != 0 {
		g.mu.Unlock()
		return "busy"
	}
	for _, roster := range teams {
		for _, p := range roster.members {
			if p.legacyQueue != q || g.carnival.entrants[p.owner.owner.CharacterID] != p {
				g.mu.Unlock()
				return "here"
			}
		}
	}
	match := &carnivalMatch{key: q.key, arena: *room.definition.Carnival, legacyID: platform.NewID()}
	for team, roster := range teams {
		for _, p := range roster.members {
			p.team, p.match, p.legacyQueue = byte(team), match, nil
			match.members = append(match.members, p)
		}
	}
	delete(g.carnival.legacy, q.key)
	room.carnival = match
	g.mu.Unlock()
	go s.startCarnivalMatch(context.WithoutCancel(ctx), match)
	return "accepted"
}

func (s *Server) carnivalLegacyPartyCurrent(roster *carnivalLegacyRoster) bool {
	s.parties.mu.Lock()
	defer s.parties.mu.Unlock()
	peer := s.parties.peers[roster.leader]
	if peer == nil || peer.party.ID != roster.partyID || peer.party.LeaderID != roster.leader ||
		len(peer.party.Members) != len(roster.members) {
		return false
	}
	for _, player := range roster.members {
		if _, found := peer.party.Member(player.owner.owner.CharacterID); !found {
			return false
		}
	}
	return true
}

func (s *Server) resetCarnivalLegacyQueue(ctx context.Context, q *carnivalLegacyQueue) {
	g := s.gameplay
	g.mu.Lock()
	if g.carnival.legacy[q.key] != q {
		g.mu.Unlock()
		return
	}
	delete(g.carnival.legacy, q.key)
	for _, team := range []*carnivalLegacyRoster{q.first, q.second} {
		if team != nil {
			for _, p := range team.members {
				p.legacyQueue, p.waitMap = nil, content.CarnivalSecondLobby
			}
		}
	}
	g.mu.Unlock()
	for _, p := range q.first.members {
		s.carnivalWarp(ctx, p, q.key.mapID-100, content.CarnivalSecondLobby, "st00")
	}
}

func (s *Server) expireCarnivalLegacyQueues(ctx context.Context, now time.Time) {
	g := s.gameplay
	g.mu.Lock()
	var expired []*carnivalLegacyQueue
	for _, queue := range g.carnival.legacy {
		if !now.Before(queue.deadline) {
			expired = append(expired, queue)
		}
	}
	g.mu.Unlock()
	for _, queue := range expired {
		go s.resetCarnivalLegacyQueue(ctx, queue)
	}
}

// Piggy2 scripts/npc/2042007.js provides the legacy team rank payout.
func carnivalLegacyExperience(score int, winner bool) int64 {
	exp := int64(15000)
	switch {
	case score > 500:
		exp = 48000
	case score > 250:
		exp = 35000
	case score > 100:
		exp = 25000
	}
	if !winner {
		exp /= 2
	}
	return exp
}

func (s *Server) leaveCarnivalLegacy(ctx context.Context, snapshot domain.CharacterSession) (string, error) {
	g := s.gameplay
	mapID, cid := snapshot.Character.MapID, snapshot.CharacterID
	g.mu.Lock()
	p := g.carnival.entrants[cid]
	if p == nil || !p.legacy || !sameSceneOwner(p.owner.owner, snapshot) {
		g.mu.Unlock()
		return "unavailable", nil
	}
	queue, match := p.legacyQueue, p.match
	var exp int64
	winner := false
	to := content.CarnivalSecondLobby
	if mapID == content.CarnivalSecondLobby {
		to = p.home
	} else if match != nil && match.collected && (mapID == match.arena.WinnerMap || mapID == match.arena.LoserMap) {
		winner = match.totals[p.team] > match.totals[1-p.team]
		if match.totals[p.team] > 0 {
			exp = carnivalLegacyExperience(match.totals[p.team], winner)
		}
		to = content.CarnivalSecondExit
	} else if queue == nil && mapID != content.CarnivalSecondExit {
		g.mu.Unlock()
		return "unavailable", nil
	}
	g.mu.Unlock()
	if queue != nil {
		s.resetCarnivalLegacyQueue(ctx, queue)
		if mapID != content.CarnivalSecondLobby {
			return "left", nil
		}
	}
	if exp != 0 {
		outcome := "carnival2-losses"
		if winner {
			outcome = "carnival2-wins"
		}
		err := s.applyEventExchange(ctx, eventOccupant{p.owner.live, p.owner.sink}, "carnival2-reward-"+match.legacyID,
			func(current domain.CharacterSession) (domain.ScriptExchange, error) {
				if current.Character.MapID != mapID || !sameSceneOwner(p.owner.owner, current) {
					return domain.ScriptExchange{}, domain.ErrForbidden
				}
				return domain.ScriptExchange{EXP: exp, Logs: []domain.ScriptLogChange{{
					Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: outcome}, Delta: 1,
				}}}, nil
			})
		if err != nil {
			return "unavailable", err
		}
	}
	if !s.carnivalWarp(ctx, p, mapID, to, "") {
		return "unavailable", nil
	}
	g.mu.Lock()
	if to == p.home {
		delete(g.carnival.entrants, cid)
	} else if to == content.CarnivalSecondLobby {
		p.match, p.waitMap, p.dead, p.earned, p.available = nil, to, false, 0, 0
	}
	g.mu.Unlock()
	return "left", nil
}

func (a *worldScript) carnivalLegacyCommand(ctx context.Context, snapshot domain.CharacterSession, cmd scripting.Command) (scripting.CommandResult, error) {
	s := a.server
	if cmd.Kind == "carnival_ii_enabled" {
		return scripting.CommandResult{Value: s.carnivalLegacyEnabled()}, nil
	}
	if !s.carnivalLegacyEnabled() || !a.hasNPCContext(snapshot) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	var value string
	var err error
	switch cmd.Kind {
	case "carnival_ii_enter":
		if cmd.Script != "npc/9010022.lua" || a.npcID != 9010022 {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		value = s.enterCarnivalLegacy(ctx, a, snapshot)
	case "carnival_ii_register":
		if cmd.Script != "npc/carnival_ii_lobby.lua" || a.npcID != 2042005 || len(cmd.Ints) != 1 {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		value = s.registerCarnivalLegacy(ctx, a, snapshot, int(cmd.Ints[0]))
	case "carnival_ii_accept":
		if cmd.Script != "npc/carnival_ii_wait.lua" || a.npcID != 2042008 {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		value = s.acceptCarnivalLegacy(ctx, snapshot)
	case "carnival_ii_exit":
		mapID := snapshot.Character.MapID
		if !content.CarnivalSecondField(mapID) || a.npcID != 2042005 && a.npcID != 2042007 && a.npcID != 2042008 {
			return scripting.CommandResult{}, domain.ErrForbidden
		}
		value, err = s.leaveCarnivalLegacy(ctx, snapshot)
	}
	return scripting.CommandResult{Value: value}, err
}
