package world

import (
	"math/rand/v2"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Phantom cards (卡牌：白 24100003, 卡牌：黑 24120002). Legacy
// MapleCharacter.handleCardStack: every monster a Phantom hits with another
// skill rolls the learned card skill's prop%; a success adds a card to the
// deck and throws one at that monster. The v148 client then attacks with the
// card skill itself (0x54C65F) and reports the thrown card on 0xD0.

const (
	skillCarteBlanche = 24100003
	skillCarteNoir    = 24120002
)

// phantomDeckSize is the deck the client shows: 40 cards for the fourth job.
func phantomDeckSize(job int) int {
	if job == 2412 {
		return 40
	}
	return 20
}

// phantomCardSkill is the card skill a Phantom throws and its level.
func phantomCardSkill(c domain.Character, now time.Time) (id, level int) {
	if level = skills.EffectiveLevel(c, skillCarteNoir, now); level > 0 {
		return skillCarteNoir, level
	}
	return skillCarteBlanche, skills.EffectiveLevel(c, skillCarteBlanche, now)
}

// phantomCards runs after an applied attack of m.
func (s *Server) phantomCards(m *skillMember, e *combat.AttackEvent) {
	c := e.Session.Character
	if !domain.IsPhantomJob(c.Job) || len(e.Targets) == 0 || e.SkillID == skillCarteBlanche || e.SkillID == skillCarteNoir {
		return
	}
	id, level := phantomCardSkill(c, e.Now)
	if level < 1 {
		return
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil || def.Disabled {
		return
	}
	values, err := def.Values(level)
	if err != nil || values["prop"] < 1 {
		return
	}
	colour := 1
	if id == skillCarteNoir {
		colour = 2
	}
	for _, target := range e.Targets {
		if rand.IntN(100) >= values["prop"] {
			continue
		}
		s.skills.mu.Lock()
		if m.cards < phantomDeckSize(c.Job) {
			m.cards++
		}
		m.cardKey++
		deck, key := m.cards, m.cardKey
		s.skills.mu.Unlock()
		if m.sink.WritePacket(protocol.PhantomCard(c.ID, target, id, key, colour)) != nil {
			return
		}
		_ = m.sink.WritePacket(protocol.PhantomDeck(deck))
	}
}

// 卡牌審判 (20031209, fourth job 20031210) spends a full deck and throws u
// cards at the monsters around the Phantom.
func phantomJudgment(skillID int) bool { return skillID == 20031209 || skillID == 20031210 }

// phantomDeckFull reports whether m may cast 卡牌審判 now.
func (s *Server) phantomDeckFull(m *skillMember, job int) bool {
	s.skills.mu.Lock()
	defer s.skills.mu.Unlock()
	return domain.IsPhantomJob(job) && m.cards >= phantomDeckSize(job)
}

// phantomDraw empties the deck after a committed 卡牌審判 and throws its cards.
func (s *Server) phantomDraw(m *skillMember, c domain.Character, skillID int, now time.Time) {
	s.skills.mu.Lock()
	m.cards = 0
	s.skills.mu.Unlock()
	_ = m.sink.WritePacket(protocol.PhantomDeck(0))
	card, level := phantomCardSkill(c, now)
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID)
	if err != nil || level < 1 {
		return
	}
	values, err := def.Values(1)
	if err != nil {
		return
	}
	var near []int32
	for _, mob := range s.triggerMobs(m.channel, c.MapID) {
		if dx, dy := mob.X-c.X, mob.Y-c.Y; dx >= -500 && dx <= 500 && dy >= -300 && dy <= 300 {
			near = append(near, mob.ObjectID)
		}
	}
	colour := 1
	if card == skillCarteNoir {
		colour = 2
	}
	for i := 0; i < values["u"] && len(near) > 0; i++ {
		s.skills.mu.Lock()
		m.cardKey++
		key := m.cardKey
		s.skills.mu.Unlock()
		if m.sink.WritePacket(protocol.PhantomCard(c.ID, near[i%len(near)], card, key, colour)) != nil {
			return
		}
	}
}
