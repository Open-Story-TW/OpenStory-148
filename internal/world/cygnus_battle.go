package world

import (
	"errors"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

const (
	cygnusArena       = 271040100
	cygnusIntro       = 8850012
	cygnusBody        = 8850011
	cygnusKnightStart = 8850000
	cygnusKnightCount = 5

	cygnusIntroTime      = 5 * time.Second
	cygnusKnightInterval = 150 * time.Second
)

type cygnusBattle struct {
	stats          map[int]content.Mob
	intro          *gameplayMob
	body           *gameplayMob
	introUntil     time.Time
	nextKnight     time.Time
	knights        [cygnusKnightCount]int
	summoned       int
	regular        *gameplayMob
	awakened       []*gameplayMob
	defendingHP    int64
	awakenedFailed bool
}

func (g *gameplaySpace) prepareCygnusBattle(room *gameplayRoom) error {
	battle := &cygnusBattle{stats: make(map[int]content.Mob, cygnusKnightCount*2+2)}
	for id := cygnusKnightStart; id <= cygnusBody-2; id++ {
		mob, err := g.catalog.Mob(id)
		if err != nil {
			return err
		}
		battle.stats[id] = mob
	}
	for _, id := range []int{cygnusIntro, cygnusBody} {
		mob, err := g.catalog.Mob(id)
		if err != nil {
			return err
		}
		battle.stats[id] = mob
	}
	for i := range battle.knights {
		battle.knights[i] = cygnusKnightStart + i
	}
	rand.Shuffle(len(battle.knights), func(i, j int) {
		battle.knights[i], battle.knights[j] = battle.knights[j], battle.knights[i]
	})
	battle.intro = g.newCygnusMob(battle.stats[cygnusIntro], -363)
	battle.body = g.newCygnusMob(battle.stats[cygnusBody], -363)
	if battle.intro == nil || battle.body == nil {
		return errors.New("unable to allocate Cygnus")
	}
	battle.intro.fake = true
	battle.defendingHP = battle.body.stats.HP / 2
	room.cygnus = battle
	return nil
}

func (g *gameplaySpace) newCygnusMob(stats content.Mob, x int) *gameplayMob {
	oid := g.alloc()
	if oid == 0 {
		return nil
	}
	life := content.Life{Kind: "m", TemplateID: stats.ID, X: x, Y: 115, Foothold: 1, RespawnSeconds: -1}
	return &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
}

func (s *Server) startCygnusBattle(attempt *eventInstance) {
	key := mapKey{channel: attempt.channel, mapID: cygnusArena}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for now := time.Now(); ; now = <-ticker.C {
			packets, active := s.advanceCygnus(key, attempt, now)
			for _, packet := range packets {
				s.broadcastInventoryPacket(key.channel, key.mapID, packet)
			}
			if !active {
				return
			}
		}
	}()
}

func (s *Server) advanceCygnus(key mapKey, attempt *eventInstance, now time.Time) ([][]byte, bool) {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[key]
	if room == nil || room.cygnus == nil {
		return nil, false
	}
	s.events.mu.Lock()
	active := !attempt.starting && !attempt.ended && !attempt.cleared &&
		s.events.running[eventKey{key.channel, "Cygnus"}] == attempt && len(attempt.members) >= attempt.definition.Keep
	s.events.mu.Unlock()
	if !active {
		return nil, false
	}
	return g.advanceCygnusLocked(room, now), true
}

func (g *gameplaySpace) advanceCygnusLocked(room *gameplayRoom, now time.Time) [][]byte {
	fight := room.cygnus
	place := func(m *gameplayMob) []byte {
		room.mobs[m.objectID] = m
		return m.spawnPacket(false, now)
	}
	if fight.introUntil.IsZero() {
		fight.introUntil = now.Add(cygnusIntroTime)
		return [][]byte{place(fight.intro)}
	}
	if now.Before(fight.introUntil) {
		return nil
	}
	var packets [][]byte
	if room.mobs[fight.body.objectID] == nil {
		delete(room.mobs, fight.intro.objectID)
		fight.body.damageFloor = fight.defendingHP
		packets = append(packets, protocol.RemoveMonster(fight.intro.objectID), place(fight.body))
	}
	body := fight.body
	if fight.regular != nil && (fight.regular.hp > 0 || fight.regular.claiming) {
		return packets
	}
	fight.regular = nil
	// v148 MobSkill 200/221 selects one of the five knights on a 150-second
	// interval. 200/222 needs five earlier casts and at most half boss HP.
	if fight.summoned < len(fight.knights) && !now.Before(fight.nextKnight) {
		id := fight.knights[fight.summoned]
		knight := g.newCygnusMob(fight.stats[id], -245)
		if knight == nil {
			return packets
		}
		fight.nextKnight = now.Add(cygnusKnightInterval)
		fight.summoned++
		fight.regular = knight
		body.damageFloor = body.hp
		return append(packets, place(knight), protocol.PortalTrainingMessage("西格諾斯召喚了一名騎士團長。"))
	}
	if fight.summoned == len(fight.knights) && body.hp <= fight.defendingHP && len(fight.awakened) == 0 && !fight.awakenedFailed {
		wave := make([]*gameplayMob, 0, cygnusKnightCount)
		for i := range cygnusKnightCount {
			m := g.newCygnusMob(fight.stats[cygnusKnightStart+cygnusKnightCount+i], -563+i*100)
			if m == nil {
				fight.awakenedFailed = true
				return packets
			}
			wave = append(wave, m)
		}
		fight.awakened = wave
		for _, knight := range wave {
			packets = append(packets, place(knight))
		}
		return append(packets, protocol.PortalTrainingMessage("覺醒的五名騎士團長出現了！"))
	}
	body.damageFloor = fight.defendingHP
	if len(fight.awakened) != 0 {
		for _, knight := range fight.awakened {
			if knight.hp > 0 || knight.claiming {
				return packets
			}
		}
		if body.damageFloor != 0 {
			packets = append(packets, protocol.PortalTrainingMessage("騎士團長全部倒下，現在可以擊敗西格諾斯。"))
		}
		fight.defendingHP, body.damageFloor = 0, 0
	}
	return packets
}

func (s *Server) finishCygnusBattle(key mapKey, actor int64) {
	s.events.mu.Lock()
	attempt := s.events.byMember[actor]
	won := attempt != nil && attempt.definition.Name == "Cygnus" && attempt.channel == key.channel &&
		!attempt.starting && !attempt.cleared && !attempt.ended
	if won {
		attempt.cleared = true
	}
	s.events.mu.Unlock()
	if won {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗西格諾斯！請拾取獎勵，再從左側傳送點離開。"))
	}
}
