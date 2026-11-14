package world

import (
	"time"

	"maple-go-server/internal/protocol"
)

// mobHoldTerm bounds a QA !mobhold: a test run that ends without !mobhold 0
// (crash, timeout) must not leave a field where nobody can kill anything.
const mobHoldTerm = 15 * time.Minute

// holdFieldMobs marks every mob of one field (QA !mobhold). Held mobs take
// damage but keep 1 HP, so attack-applied statuses stay readable. The hold
// lapses after mobHoldTerm.
func (s *Server) holdFieldMobs(key mapKey, held bool) {
	s.holdFieldMobsAt(key, held, time.Now())
}

func (s *Server) holdFieldMobsAt(key mapKey, held bool, now time.Time) {
	if s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.rooms[key]; r != nil {
		for _, mob := range r.mobs {
			mob.qaHeld, mob.qaHeldUntil = held, now.Add(mobHoldTerm)
		}
	}
}

// refillHeldMobs restores held mobs between settlements and releases holds
// past their term. Contributions reset with HP, keeping the settlement
// invariant max-before+damage == contributed.
func (g *gameplaySpace) refillHeldMobs() {
	g.refillHeldMobsAt(time.Now())
}

func (g *gameplaySpace) refillHeldMobsAt(now time.Time) {
	type delivery struct {
		sink   packetSink
		packet []byte
	}
	var out []delivery
	g.mu.Lock()
	for _, r := range g.rooms {
		for _, mob := range r.mobs {
			if mob.qaHeld && !now.Before(mob.qaHeldUntil) {
				mob.qaHeld = false
			}
			if !mob.qaHeld || mob.hp <= 0 || mob.hp >= mob.stats.HP || mob.claiming || mob.reservation != nil {
				continue
			}
			mob.hp, mob.contributions = mob.stats.HP, nil
			for _, m := range r.members {
				out = append(out, delivery{m.sink, protocol.MonsterHP(mob.objectID, mob.hp, mob.stats.HP)})
			}
		}
	}
	g.mu.Unlock()
	for _, d := range out {
		_ = d.sink.WritePacket(d.packet)
	}
}
