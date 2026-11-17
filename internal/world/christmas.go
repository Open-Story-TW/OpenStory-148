package world

import (
	"context"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type christmasProgress int

const (
	christmasDefending christmasProgress = iota
	christmasSummoning
	christmasBoss
	christmasRewarding
)

func (s *Server) startChristmasBoss(inst *eventInstance, now time.Time) bool {
	event, ok := content.ChristmasByName(inst.definition.Name)
	if !ok {
		return false
	}
	s.events.mu.Lock()
	if inst.ended || inst.christmas == christmasRewarding || now.Before(inst.deadline) {
		s.events.mu.Unlock()
		return true
	}
	if inst.christmas != christmasDefending {
		s.events.mu.Unlock()
		return false
	}
	inst.christmas = christmasSummoning
	s.events.mu.Unlock()

	game := s.gameplay
	stats, err := game.catalog.Mob(event.Boss)
	if err != nil {
		return false
	}
	game.mu.Lock()
	room := game.rooms[mapKey{inst.channel, event.Maps[0]}]
	if room == nil {
		game.mu.Unlock()
		return false
	}
	alive := false
	for _, mob := range room.mobs {
		if mob.claiming || mob.reservation != nil {
			game.mu.Unlock()
			s.events.mu.Lock()
			inst.christmas = christmasDefending
			s.events.mu.Unlock()
			return true
		}
		alive = alive || event.IsSnowman(mob.life.TemplateID) && mob.hp > 0
	}
	oid := game.alloc()
	if !alive || oid == 0 {
		game.mu.Unlock()
		return false
	}
	var packets [][]byte
	for id, mob := range room.mobs {
		if mob.hp > 0 {
			packets = append(packets, protocol.KillMonster(id))
		}
	}
	clear(room.mobs)
	life := content.ChristmasMob(event.Boss)
	boss := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
	room.mobs[oid] = boss
	packets = append(packets, protocol.SpawnMonster(oid, life, false), protocol.Clock(event.Seconds))
	var visits []*sceneVisit
	var controller *sceneVisit
	for id, member := range room.members {
		visits = append(visits, member.visit)
		if boss.controller == 0 {
			boss.controller = id
			controller = member.visit
		}
	}
	game.mu.Unlock()

	s.events.mu.Lock()
	if inst.ended {
		s.events.mu.Unlock()
		return true
	}
	inst.christmas = christmasBoss
	inst.deadline = now.Add(time.Duration(event.Seconds) * time.Second)
	s.events.mu.Unlock()
	for _, visit := range visits {
		_ = s.writeScenePackets(visit, packets...)
	}
	if controller != nil {
		_ = s.writeScenePackets(controller, protocol.SpawnMonster(oid, life, true))
	}
	return true
}

func (s *Server) christmasFriendlyDamage(live *liveCharacter, sink packetSink, payload []byte) error {
	if len(payload) != 12 || s.events == nil {
		return nil
	}
	r := protocol.NewReader(payload)
	attacker, actor, defender := r.I32(), r.I32(), r.I32()
	session := partySession(live)
	visit := s.sceneFor(live, sink)
	if visit == nil || session.Character.ID != int64(actor) {
		return nil
	}
	event, ok := content.ChristmasByMap(session.Character.MapID)
	if !ok {
		return nil
	}
	s.events.mu.Lock()
	inst := s.events.byMember[session.CharacterID]
	active := inst != nil && inst.channel == session.ChannelID && inst.definition.Name == event.Name &&
		!inst.ended && inst.christmas == christmasDefending
	s.events.mu.Unlock()
	if !active {
		return nil
	}
	game := s.gameplay
	now := time.Now()
	game.mu.Lock()
	room := game.rooms[mapKey{session.ChannelID, session.Character.MapID}]
	if room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		game.mu.Unlock()
		return nil
	}
	from, to := room.mobs[attacker], room.mobs[defender]
	if from == nil || from.controller != session.CharacterID || from.hp <= 0 || event.IsSnowman(from.life.TemplateID) ||
		to == nil || to.hp <= 0 || !event.IsSnowman(to.life.TemplateID) ||
		abs(from.life.X-to.life.X) > 600 || abs(from.life.Y-to.life.Y) > 350 ||
		now.Sub(from.friendlyAttackAt) < 400*time.Millisecond {
		game.mu.Unlock()
		return nil
	}
	from.friendlyAttackAt = now
	level := max(1, to.stats.Level)
	damage := min(to.hp, int64(level*rand.IntN(level)/2))
	to.hp -= damage
	remaining, maximum := to.hp, to.stats.HP
	visits := make([]*sceneVisit, 0, len(room.members))
	for _, member := range room.members {
		visits = append(visits, member.visit)
	}
	packets := [][]byte{protocol.FriendlyDamage(defender, int(damage), int(remaining), int(maximum))}
	failed := remaining == 0
	var owner *sceneVisit
	var controller []byte
	if next := to.life.TemplateID - 1; failed && event.IsSnowman(next) {
		// Each difficulty has five successive WZ forms. Advance within
		// that family's range even where the hard-mode WZ links to itself.
		stats, err := game.catalog.Mob(next)
		if id := game.alloc(); err == nil && id != 0 {
			life := to.origin
			life.TemplateID = next
			stats.Invincible = true
			mob := &gameplayMob{objectID: id, life: life, origin: life, stats: stats, hp: stats.HP}
			for id, member := range room.members {
				mob.controller, owner = id, member.visit
				break
			}
			delete(room.mobs, defender)
			room.mobs[id] = mob
			packets = append(packets, protocol.KillMonster(defender), protocol.SpawnMonster(id, life, false))
			controller = protocol.SpawnMonster(id, life, true)
			failed = false
		}
	}
	game.mu.Unlock()
	for _, observer := range visits {
		_ = s.writeScenePackets(observer, packets...)
	}
	if owner != nil {
		_ = s.writeScenePackets(owner, controller)
	}
	if failed {
		s.eventEnd(inst)
	}
	return nil
}

func (a *worldScript) finishChristmas(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	result := func(text string) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: text}, nil
	}
	if a.npcID != 9105004 || command.Text != "" || len(command.Ints) != 0 {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	s := a.server
	event, known := content.ChristmasByMap(snapshot.Character.MapID)
	inst := s.eventOf(snapshot.CharacterID)
	if !known || inst == nil || s.partySnapshotOf(snapshot.CharacterID).LeaderID != snapshot.CharacterID {
		return result("leader")
	}
	s.events.mu.Lock()
	if inst.ended || inst.christmas != christmasBoss || len(inst.members) < event.PartyMin {
		s.events.mu.Unlock()
		return result("defend")
	}
	inst.christmas = christmasRewarding
	s.events.mu.Unlock()
	defer func() {
		s.events.mu.Lock()
		if !inst.ended {
			inst.christmas = christmasBoss
		}
		s.events.mu.Unlock()
	}()
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[mapKey{inst.channel, snapshot.Character.MapID}]
	defeated := false
	if room != nil {
		for _, mob := range room.mobs {
			if mob.life.TemplateID == event.Boss {
				defeated = mob.hp <= 0 && !mob.claiming && mob.reservation == nil
				break
			}
		}
	}
	game.mu.Unlock()
	if !defeated {
		return result("boss")
	}
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	for _, occupant := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		if err := s.awardChristmas(ctx, repo, inst, event, occupant); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	s.eventEndAt(inst, event.RewardMap)
	return result("clear")
}

func (s *Server) awardChristmas(ctx context.Context, repo domain.ScriptRepository, inst *eventInstance, event content.ChristmasEvent, occupant eventOccupant) error {
	occupant.live.persistMu.Lock()
	defer occupant.live.persistMu.Unlock()
	session := s.skillSession(occupant.live)
	visit := s.sceneFor(occupant.live, occupant.sink)
	if visit == nil || s.eventOf(session.CharacterID) != inst || !event.Stage(session.Character.MapID) {
		return nil
	}
	value, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session: session, OperationID: "event-" + inst.id + "-christmas-clear", EXP: event.EXP, EventCredit: event.Credit,
	})
	if err != nil {
		return err
	}
	s.applyProgressResult(occupant.live, value.Character)
	if value.Replayed {
		return nil
	}
	return s.writeScenePackets(visit, protocol.UpdateCombatStats(value.Character), protocol.ShowExperienceGain(event.EXP, true),
		protocol.PortalTrainingMessage("已領取聖誕活動經驗值及點數。"))
}
