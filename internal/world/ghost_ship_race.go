package world

import (
	"context"
	"strconv"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) ghostPortal(ctx context.Context, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	switch portal.Script {
	case "ghostShip_next", "GS_revive", "GS_stage5_water", "GS_backSpace":
	default:
		return false, nil
	}
	actor := partySession(live)
	from := actor.Character.MapID
	room, known := content.GhostRoomOf(from)
	if !known {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ghost == nil || inst.channel != actor.ChannelID {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if portal.Script == "GS_backSpace" {
		return true, eventPortalNotice(sink, "這個傳送點無法返回前一關。")
	}
	g := inst.ghost
	g.mu.Lock()
	team, found := g.teamOf(actor.CharacterID)
	if !found || g.moving || g.phase < ghostRace || g.phase == ghostFinished || !time.Now().Before(inst.ghostEndTime()) {
		g.mu.Unlock()
		return true, sink.WritePacket(protocol.EnableActions())
	}
	stage := g.stage[team]
	one := []eventOccupant{{live, sink}}
	switch portal.Script {
	case "GS_stage5_water":
		g.mu.Unlock()
		if from != room.Course(team, content.GhostStages-1) {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		return true, s.ghostWarpGroup(ctx, inst, from, one, from, "st00", 0)
	case "GS_revive":
		to, name := room.Course(team, stage), ghostStagePortal(team, stage)
		if g.phase == ghostBoss {
			to, name = room.Boss(), ghostTeamNames[team]
		}
		g.mu.Unlock()
		if from != room.Revive() && from != room.BossRevive() {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		return true, s.ghostWarpGroup(ctx, inst, from, one, to, name, 0)
	}
	if g.phase != ghostRace || from != room.Course(team, stage) || s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID || g.team[team].leader != actor.CharacterID {
		g.mu.Unlock()
		return true, eventPortalNotice(sink, "請由隊長帶領隊伍前往下一關。")
	}
	if s.mapMonsterCount(inst.channel, from) != 0 {
		g.mu.Unlock()
		return true, eventPortalNotice(sink, "請先擊敗本區剩下的怪物。")
	}
	if stage == content.GhostStages-1 && !s.gameplay.ghostDeckReady(inst.channel, from, time.Now()) {
		g.mu.Unlock()
		return true, eventPortalNotice(sink, "請讓兩邊的開關同時亮起，再從這裡前往船長室。")
	}
	group := s.activeEventMembers(inst, from)
	if len(group) != content.GhostTeamSize {
		g.mu.Unlock()
		return true, eventPortalNotice(sink, "請集合三名隊員再前往下一關。")
	}
	g.moving = true
	to := room.Course(team, stage+1)
	if stage < content.GhostStages-1 {
		g.stage[team]++
		g.mu.Unlock()
		if err := s.ghostWarpGroup(ctx, inst, from, group, to, ghostStagePortal(team, stage+1), 0); err != nil {
			s.eventEnd(inst)
			return true, err
		}
	} else {
		g.phase = ghostBoss
		g.damage[team] = 100000 // First team to reach the captain gets 5% of its finish threshold.
		g.mu.Unlock()
		if err := s.ghostBeginBoss(ctx, inst); err != nil {
			s.eventEnd(inst)
			return true, err
		}
	}
	g.mu.Lock()
	g.moving = false
	g.mu.Unlock()
	s.ghostBroadcast(inst)
	return true, nil
}

func ghostStagePortal(team, stage int) string {
	switch stage {
	case 0:
		return ghostTeamNames[team]
	case content.GhostStages - 1:
		return "st00"
	default:
		return "left00"
	}
}

func (s *Server) ghostBeginBoss(ctx context.Context, inst *eventInstance) error {
	g := inst.ghost
	rosters := g.team
	seen := [content.GhostTeams]int{}
	for _, peer := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		id := partySession(peer.live).CharacterID
		for team, roster := range rosters {
			if !roster.contains(id) || s.eventOf(id) != inst {
				continue
			}
			from := partySession(peer.live).Character.MapID
			if err := s.ghostWarpGroup(ctx, inst, from, []eventOccupant{peer}, g.room.Boss(), ghostTeamNames[team], 0); err != nil {
				return err
			}
			seen[team]++
		}
	}
	for _, n := range seen {
		if n != content.GhostTeamSize {
			return domain.ErrStaleOwner
		}
	}
	return nil
}

func (s *Server) ghostCombat(t *combatTicket) {
	if _, ok := content.GhostRoomOf(t.key.mapID); !ok {
		return
	}
	id := t.actorSession.CharacterID
	inst := s.eventOf(id)
	if inst == nil || inst.ghost == nil || inst.channel != t.key.channel {
		return
	}
	g := inst.ghost
	g.mu.Lock()
	team, found := g.teamOf(id)
	if !found || g.phase < ghostRace || g.phase == ghostFinished || !time.Now().Before(inst.ghostEndTime()) {
		g.mu.Unlock()
		return
	}
	updated, winner := false, -1
	for _, q := range t.request.Targets {
		if g.phase == ghostRace && q.Defeated {
			g.points[team]++
			updated = true
		}
		if g.phase != ghostBoss || t.key.mapID != g.room.Boss() || q.MobID != content.GhostBoss || q.Damage <= 0 {
			continue
		}
		g.damage[team] += int64(q.Damage)
		g.total[id] += int64(q.Damage)
		updated = true
		if q.Defeated || g.damage[team] >= 2_000_000 {
			winner = team
		}
	}
	if winner >= 0 {
		g.phase = ghostFinished
	}
	g.mu.Unlock()
	if !updated {
		return
	}
	s.ghostBroadcast(inst)
	if winner >= 0 {
		go s.finishGhostMatch(inst, winner)
	}
}

func (s *Server) finishGhostMatch(inst *eventInstance, winner int) {
	g := inst.ghost
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	group := s.activeEventMembers(inst, g.room.Boss())
	if len(group) != content.GhostTeamSize*content.GhostTeams {
		s.eventEnd(inst)
		return
	}
	g.mu.Lock()
	points, damage := g.points, g.total
	teams := g.team
	g.mu.Unlock()
	for _, peer := range group {
		id := partySession(peer.live).CharacterID
		team := 0
		if teams[1].contains(id) {
			team = 1
		}
		stat := "ghost-ship-loss"
		fragments := 1
		if team == winner {
			stat = "ghost-ship-win"
			fragments = 2
		}
		err := s.applyEventExchange(ctx, peer, "ghost-ship-clear-"+inst.id, func(actor domain.CharacterSession) (domain.ScriptExchange, error) {
			if actor.CharacterID != id || actor.Character.MapID != g.room.Boss() || s.eventOf(id) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{EXP: points[team]*20 + damage[id]/100,
				Items: []domain.ScriptItemDelta{{TemplateID: content.GhostFragment, Quantity: fragments}},
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: stat}, Delta: 1}}}, nil
		})
		if err != nil {
			s.eventEnd(inst)
			return
		}
	}
	s.events.mu.Lock()
	if inst.ended {
		s.events.mu.Unlock()
		return
	}
	s.closeEventMembersLocked(inst)
	s.events.mu.Unlock()
	for _, peer := range group {
		id := partySession(peer.live).CharacterID
		destination := inst.definition
		destination.ExitMap = content.GhostLoser
		if teams[winner].contains(id) {
			destination.ExitMap = content.GhostWinner
		}
		s.eventExit(inst.channel, destination, peer.live, peer.sink)
		if partySession(peer.live).Character.MapID == destination.ExitMap {
			team := 0
			if teams[1].contains(id) {
				team = 1
			}
			course := points[team] * 20
			bonus := damage[id] / 100
			_ = peer.sink.WritePacket(protocol.SessionValue("PRaid_Team", ""))
			_ = peer.sink.WritePacket(protocol.SessionValue("PRaid_Point", strconv.FormatInt(course, 10)))
			_ = peer.sink.WritePacket(protocol.SessionValue("PRaid_Bonus", strconv.FormatInt(bonus, 10)))
			_ = peer.sink.WritePacket(protocol.SessionValue("PRaid_Total", strconv.FormatInt(course+bonus, 10)))
		}
	}
	s.events.mu.Lock()
	key := eventKey{inst.channel, inst.definition.Name}
	if s.events.running[key] == inst {
		delete(s.events.running, key)
	}
	s.events.mu.Unlock()
}
