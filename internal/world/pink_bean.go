package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const (
	pinkBeanArena    = 270050100
	pinkBeanNPC      = 2141000
	pinkBeanAltar    = 2709000
	pinkBeanOffering = 4032002
	pinkBeanBody     = 8820001
	pinkBeanPoint    = 5
	pinkBeanGround   = -42
)

type pinkBeanWave struct {
	enemies []int
	scenery []int
}

// The reset and revive lists in v148 WZ put the real statues and the stationary
// versions in the same place. The visible statue parts supply the shared HP
// for each wave: their HP sums to that of its transparent WZ marker.
var pinkBeanWaves = []pinkBeanWave{
	{scenery: []int{8820008}},
	{scenery: []int{8820009, 8820000, 8820019, 8820021, 8820022, 8820023}},
	{enemies: []int{8820003}, scenery: []int{8820000, 8820019, 8820021, 8820022, 8820023}},
	{enemies: []int{8820003, 8820004}, scenery: []int{8820000, 8820019, 8820026, 8820027}},
	{enemies: []int{8820003, 8820004, 8820005}, scenery: []int{8820000, 8820019, 8820027}},
	{enemies: []int{8820003, 8820004, 8820005, 8820006}, scenery: []int{8820000, 8820019}},
	{enemies: []int{8820015, 8820016, 8820017, 8820018, 8820002}, scenery: []int{8820000}},
	{enemies: []int{pinkBeanBody}},
}

type pinkBeanBattle struct {
	altar     *roomReactor
	offering  bool
	phase     int
	stats     map[int]content.Mob
	sceneMobs []*gameplayMob
	targets   map[int32]*gameplayMob
}

func (g *gameplaySpace) preparePinkBeanBattle(room *gameplayRoom) error {
	r := &roomReactor{objectID: g.alloc(), def: content.Reactor{TemplateID: pinkBeanAltar, X: 8, Y: -43, Respawn: -1}}
	if r.objectID == 0 {
		return errors.New("unable to allocate Pink Bean's altar")
	}
	shadow := &roomReactor{objectID: g.alloc(), def: content.Reactor{TemplateID: 2708000, X: 2, Y: -75, Respawn: -1}}
	if shadow.objectID == 0 {
		return errors.New("unable to allocate Pink Bean's scenery reactor")
	}
	battle := &pinkBeanBattle{altar: r, phase: -1, stats: make(map[int]content.Mob)}
	for _, wave := range pinkBeanWaves {
		for _, list := range [][]int{wave.enemies, wave.scenery} {
			for _, id := range list {
				if _, loaded := battle.stats[id]; loaded {
					continue
				}
				mob, err := g.catalog.Mob(id)
				if err != nil {
					return err
				}
				battle.stats[id] = mob
			}
		}
	}
	for _, mob := range room.mobs {
		mob.fake = true
		battle.sceneMobs = append(battle.sceneMobs, mob)
	}
	if room.reactors == nil {
		room.reactors = map[int32]*roomReactor{}
	}
	room.reactors[r.objectID], room.reactors[shadow.objectID] = r, shadow
	room.pinkBean = battle
	return nil
}

func (g *gameplaySpace) pinkBeanWaveObjects(battle *pinkBeanBattle, phase int) ([]*gameplayMob, map[int32]*gameplayMob, bool) {
	wave := pinkBeanWaves[phase]
	mobs := make([]*gameplayMob, 0, len(wave.enemies)+len(wave.scenery))
	targets := make(map[int32]*gameplayMob, len(wave.enemies))
	add := func(id int, scenery bool) bool {
		oid := g.alloc()
		if oid == 0 {
			return false
		}
		stats := battle.stats[id]
		life := content.Life{Kind: "m", TemplateID: id, X: pinkBeanPoint, Y: pinkBeanGround, Foothold: 35, RespawnSeconds: -1}
		mob := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, fake: scenery}
		mobs = append(mobs, mob)
		if !scenery {
			targets[oid] = mob
		}
		return true
	}
	for _, id := range wave.enemies {
		if !add(id, false) {
			return nil, nil, false
		}
	}
	for _, id := range wave.scenery {
		if !add(id, true) {
			return nil, nil, false
		}
	}
	return mobs, targets, true
}

func installPinkBeanWave(room *gameplayRoom, phase int, mobs []*gameplayMob, targets map[int32]*gameplayMob) [][]byte {
	battle := room.pinkBean
	var packets [][]byte
	for _, m := range battle.sceneMobs {
		delete(room.mobs, m.objectID)
		if m.hp > 0 {
			packets = append(packets, protocol.RemoveMonster(m.objectID))
		}
	}
	battle.phase, battle.sceneMobs, battle.targets = phase, mobs, targets
	for _, m := range mobs {
		room.mobs[m.objectID] = m
		packets = append(packets, m.spawnPacket(false, time.Now()))
	}
	return packets
}

func (a *worldScript) summonPinkBean(ctx context.Context, attempt *eventInstance, actor domain.CharacterSession, cmd scripting.Command) (bool, error) {
	s := a.server
	if actor.Character.MapID != pinkBeanArena || a.npcID != pinkBeanNPC || a.objectID == 0 || s.gameplay == nil {
		return false, nil
	}
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return false, domain.ErrForbidden
	}
	key := mapKey{channel: actor.ChannelID, mapID: pinkBeanArena}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.pinkBean == nil || room.pinkBean.phase >= 0 || room.pinkBean.offering ||
		room.npcs[a.objectID].TemplateID != a.npcID || !s.expeditionCombatAllowedLocked(key, actor.CharacterID) {
		g.mu.Unlock()
		return false, nil
	}
	mobs, targets, ready := g.pinkBeanWaveObjects(room.pinkBean, 0)
	if !ready {
		g.mu.Unlock()
		return false, nil
	}
	room.pinkBean.offering = true
	g.mu.Unlock()

	a.live.persistMu.Lock()
	a.live.mu.Lock()
	current := a.live.session
	current.Lease = s.currentLease()
	a.live.mu.Unlock()
	var change domain.ScriptExchangeResult
	var err error
	if current.CharacterID != actor.CharacterID || current.Generation != actor.Generation ||
		current.ChannelID != actor.ChannelID || current.Character.MapID != pinkBeanArena || s.tradeActive(a.live) {
		err = domain.ErrStaleOwner
	} else {
		change, err = repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: current, OperationID: cmd.ID,
			Items: []domain.ScriptItemDelta{{TemplateID: pinkBeanOffering, Quantity: -1}}})
		if err == nil {
			s.applyInventoryResult(a.live, change.Character)
		}
	}
	a.live.persistMu.Unlock()

	g.mu.Lock()
	room.pinkBean.offering = false
	if err != nil || change.Replayed || g.rooms[key] != room || !s.pinkBeanGroupReadyLocked(key, room, attempt) {
		g.mu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrStaleOwner) {
			return false, nil
		}
		return false, err
	}
	delete(room.npcs, a.objectID)
	altar := room.pinkBean.altar
	altar.state = 1
	packets := installPinkBeanWave(room, 0, mobs, targets)
	g.mu.Unlock()
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.RemoveNPC(a.objectID, true))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.RemoveNPC(a.objectID, false))
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.ReactorState(altar.objectID, 1, altar.def.X, altar.def.Y))
	for _, p := range packets {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	s.schedulePinkIntro(key, room, attempt, 1)
	if len(change.Changes) != 0 {
		return true, a.sink.WritePacket(protocol.ModifyInventory(change.Changes))
	}
	return true, nil
}

// A larger group can continue after the member who offered the orb leaves.
func (s *Server) pinkBeanGroupReadyLocked(key mapKey, room *gameplayRoom, attempt *eventInstance) bool {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if s.events.running[eventKey{key.channel, attempt.definition.Name}] != attempt || attempt.starting || attempt.ended {
		return false
	}
	present := 0
	for id := range room.members {
		if s.events.byMember[id] == attempt {
			present++
		}
	}
	return present >= attempt.definition.Keep
}

func (s *Server) schedulePinkIntro(key mapKey, room *gameplayRoom, attempt *eventInstance, next int) {
	time.AfterFunc(time.Second, func() {
		g := s.gameplay
		g.mu.Lock()
		if g.rooms[key] != room || room.pinkBean.phase+1 != next ||
			!s.pinkBeanGroupReadyLocked(key, room, attempt) {
			g.mu.Unlock()
			return
		}
		mobs, targets, ready := g.pinkBeanWaveObjects(room.pinkBean, next)
		if !ready {
			g.mu.Unlock()
			return
		}
		packets := installPinkBeanWave(room, next, mobs, targets)
		g.mu.Unlock()
		for _, p := range packets {
			s.broadcastInventoryPacket(key.channel, key.mapID, p)
		}
		if next == 1 {
			s.schedulePinkIntro(key, room, attempt, next+1)
		}
	})
}

func (s *Server) pinkBeanDefeat(key mapKey, actor int64, objectID int32) {
	if s.events == nil || s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.pinkBean == nil || room.pinkBean.targets[objectID] == nil || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	battle := room.pinkBean
	for _, mob := range battle.targets {
		if mob.hp > 0 || mob.claiming {
			g.mu.Unlock()
			return
		}
	}
	next := battle.phase + 1
	if next < len(pinkBeanWaves) {
		mobs, targets, ready := g.pinkBeanWaveObjects(battle, next)
		if !ready {
			g.mu.Unlock()
			return
		}
		packets := installPinkBeanWave(room, next, mobs, targets)
		g.mu.Unlock()
		for _, p := range packets {
			s.broadcastInventoryPacket(key.channel, key.mapID, p)
		}
		return
	}
	s.events.mu.Lock()
	attempt := s.events.byMember[actor]
	won := attempt != nil && !attempt.ended && !attempt.cleared
	if won {
		attempt.cleared = true
	}
	s.events.mu.Unlock()
	g.mu.Unlock()
	if won {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗皮卡啾！請拾取獎勵，再找神殿管理員離開。"))
	}
}
