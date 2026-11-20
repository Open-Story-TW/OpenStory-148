package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const (
	subwayStageTime  = 180 * time.Second
	subwayResultTime = 2 * time.Minute
)

var subwayPassDue = domain.ScriptLogKey{
	Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "subway_boarding_passes",
}

type subwayScore struct {
	hits, cools, misses int
}

func (score subwayScore) rankAndEXP() (byte, int) {
	for rank, threshold := range [...]int{2000, 1500, 1000, 500} {
		if score.hits+score.cools >= threshold {
			base := [...]int{12000, 9000, 5750, 3000}
			return byte(rank), base[rank] + score.hits + 5*score.cools
		}
	}
	return 4, 0
}

// All mutable room and score state lives under gameplay.mu. The deadline is
// also read by the generic event clock path, which holds events.mu.
type subwaySurvival struct {
	mapID, stage       int
	gauge              int
	started, lastDecay time.Time
	deadline           atomic.Int64
	scores             map[int64]subwayScore
	transferring       bool
}

func (s *Server) prepareSubwaySurvival(inst *eventInstance) error {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range inst.definition.Maps {
		room := g.rooms[mapKey{inst.channel, id}]
		if room == nil {
			return fmt.Errorf("Subway survival map %d is unavailable", id)
		}
		if content.SubwaySurvivalStage(id) && (room.definition.Subway == nil || len(room.mobs) != 50) {
			return fmt.Errorf("Subway survival map %d has incomplete v148 data", id)
		}
		room.subway = inst.subway
	}
	room := g.rooms[mapKey{inst.channel, content.SubwaySurvivalFirst}]
	inst.subway.begin(room, 1, time.Now())
	return nil
}

func (state *subwaySurvival) begin(room *gameplayRoom, stage int, now time.Time) {
	state.mapID, state.stage = room.definition.ID, stage
	state.started, state.lastDecay = now, now
	duration := subwayResultTime
	if rules := room.definition.Subway; rules != nil {
		state.gauge = rules.Gauge
		duration = subwayStageTime
	}
	state.deadline.Store(now.Add(duration).UnixNano())
}

func (s *Server) subwayEntered(inst *eventInstance, c domain.Character, sink packetSink) bool {
	state := inst.subway
	g := s.gameplay
	g.mu.Lock()
	score := state.scores[c.ID]
	stage := state.stage
	g.mu.Unlock()
	switch c.MapID {
	case content.SubwaySurvivalCleared:
		rank, exp := score.rankAndEXP()
		_ = sink.WritePacket(protocol.SubwayResult(rank, exp))
		return true
	case content.SubwaySurvivalFailed:
		_ = sink.WritePacket(protocol.SubwayResult(4, 0))
		return true
	}
	for _, packet := range subwayCounters(score) {
		_ = sink.WritePacket(packet)
	}
	_ = sink.WritePacket(protocol.SubwayCounter("massacre_laststage", stage-1))
	return false
}

func subwayCounters(score subwayScore) [][]byte {
	return [][]byte{
		protocol.SubwayCounter("massacre_hit", score.hits),
		protocol.SubwayCounter("massacre_cool", score.cools),
		protocol.SubwayCounter("massacre_miss", score.misses),
	}
}

func subwayCombatLocked(room *gameplayRoom, actorID int64, mob *gameplayMob, target domain.CombatTarget, now time.Time) bool {
	if room == nil || room.subway == nil || room.definition.Subway == nil {
		return false
	}
	state, rule := room.subway, room.definition.Subway
	if state.mapID != room.definition.ID || state.transferring || now.UnixNano() >= state.deadline.Load() {
		return false
	}
	score := state.scores[actorID]
	if target.Defeated {
		cool := false
		for _, line := range target.Lines {
			if line.Applied > 0 && line.Claimed() >= int64(mob.stats.MassacreCoolDamage) {
				cool = rand.IntN(100) < mob.stats.MassacreCoolChance
				break
			}
		}
		if cool {
			score.cools++
			state.gauge = min(rule.Gauge, state.gauge+rule.CoolGain)
		} else {
			score.hits++
			state.gauge = min(rule.Gauge, state.gauge+rule.HitGain)
		}
	} else if target.Damage == 0 && len(target.Lines) > 0 {
		score.misses++
		state.gauge = max(0, state.gauge-rule.MissCost)
	} else {
		return false
	}
	state.scores[actorID] = score
	return target.Damage == 0 && mob.stats.MassacreRemoveOnMiss
}

func (s *Server) tickSubwaySurvival(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	var active []*eventInstance
	for _, inst := range s.events.running {
		if inst.subway != nil && !inst.starting && !inst.ended {
			active = append(active, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range active {
		step, cancel := context.WithTimeout(ctx, 4*time.Second)
		s.advanceSubwaySurvival(step, inst, now)
		cancel()
	}
}

func (s *Server) advanceSubwaySurvival(ctx context.Context, inst *eventInstance, now time.Time) {
	g, state := s.gameplay, inst.subway
	g.mu.Lock()
	current := state.mapID
	room := g.rooms[mapKey{inst.channel, current}]
	if room == nil || room.subway != state || state.transferring {
		g.mu.Unlock()
		return
	}
	due := now.UnixNano() >= state.deadline.Load()
	rule := room.definition.Subway
	if rule == nil {
		g.mu.Unlock()
		if due {
			s.eventEnd(inst)
		}
		return
	}
	seconds := int(now.Sub(state.lastDecay) / time.Second)
	if seconds > 0 {
		state.lastDecay = state.lastDecay.Add(time.Duration(seconds) * time.Second)
		state.gauge = max(0, state.gauge-seconds*rule.Decay)
	}
	if state.gauge > 0 && !due {
		offset := int(state.lastDecay.Sub(state.started)/time.Second)*rule.Decay - (rule.Gauge - state.gauge)
		out := roomBroadcast(room, protocol.SubwayGauge(offset), -1)
		for id, member := range room.members {
			for _, p := range subwayCounters(state.scores[id]) {
				out = append(out, roomDelivery{member.sink, p})
			}
		}
		g.mu.Unlock()
		deliver(out)
		return
	}
	next := content.SubwaySurvivalFailed
	if state.gauge > 0 {
		next = content.SubwaySurvivalCleared
		if state.stage < 3 {
			next = inst.definition.Maps[state.stage]
		}
	}
	destination := g.rooms[mapKey{inst.channel, next}]
	if destination == nil {
		g.mu.Unlock()
		s.eventEnd(inst)
		return
	}
	entry, ok := destination.definition.Spawn()
	if !ok {
		g.mu.Unlock()
		s.eventEnd(inst)
		return
	}
	state.transferring = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		state.transferring = false
		g.mu.Unlock()
	}()
	group := s.activeEventMembers(inst, current)
	if len(group) == 0 {
		s.eventEnd(inst)
		return
	}
	if next == content.SubwaySurvivalCleared {
		for _, member := range group {
			_, exp := state.scores[partySession(member.live).CharacterID].rankAndEXP()
			if err := s.applyEventExchange(ctx, member, "subway-"+inst.id+"-clear", func(session domain.CharacterSession) (domain.ScriptExchange, error) {
				if session.ChannelID != inst.channel || session.Character.MapID != current || s.eventOf(session.CharacterID) != inst {
					return domain.ScriptExchange{}, domain.ErrStaleOwner
				}
				return domain.ScriptExchange{EXP: int64(exp), Logs: []domain.ScriptLogChange{{Key: subwayPassDue, Delta: 1}}}, nil
			}); err != nil {
				return
			}
		}
	}
	g.mu.Lock()
	state.begin(destination, state.stage+1, time.Now())
	g.mu.Unlock()
	for _, member := range group {
		s.cancelNPCScriptState(member.live, false)
		s.closeEconomyDialog(member.live)
		if err := s.warpEventMember(ctx, inst, current, member, next, entry); err != nil {
			s.eventEnd(inst)
			return
		}
	}
}
