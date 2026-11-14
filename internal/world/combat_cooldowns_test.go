package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestCommittedGenericCooldownKeepsOthersAndPublishesOnce(t *testing.T) {
	s, lives, sinks, _, oid, a := eruptionFixture(t)
	old := domain.SkillCooldown{ID: 1001, ExpiresAt: time.Now().Add(time.Hour)}
	lives[0].session.Character.SkillCooldowns = []domain.SkillCooldown{old}
	var ticket *combatTicket
	var result domain.CombatAttackResult
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		ticket = s.gameplay.rooms[mapKey{1, 40000}].areas[oid].reservation
		result = eruptionResult(q)
		// Model a partial result: the generic commit only owns its new cooldown.
		for i := range result.Characters {
			if result.Characters[i].ID == 1 {
				result.Characters[i].SkillCooldowns = result.Characters[i].SkillCooldowns[1:]
			}
		}
		return result, nil
	}}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	cds := lives[0].session.Character.SkillCooldowns
	if len(cds) != 2 || cds[0] != old || cds[1].ID != 2121003 || !cds[1].ExpiresAt.After(time.Now().Add(8*time.Second)) || sinks[0].count(0x19f) != 1 || sinks[1].count(0x19f) != 0 {
		t.Fatalf("cooldown result: %+v owner%d peer%d", cds, sinks[0].count(0x19f), sinks[1].count(0x19f))
	}
	lives[0].persistMu.Lock()
	s.commitCombatTicket(ticket, result)
	s.publishCombatProgressLocked(ticket, result)
	lives[0].persistMu.Unlock()
	if sinks[0].count(0x19f) != 1 || len(lives[0].session.Character.SkillCooldowns) != 2 {
		t.Fatal("replayed result repeated cooldown or reset other row")
	}
}

func TestCommittedGenericCooldownMissingExpiredOrOlderResultDoesNotReset(t *testing.T) {
	now := time.Now()
	existing := domain.SkillCooldown{ID: 2121003, ExpiresAt: now.Add(20 * time.Second)}
	for _, rows := range [][]domain.SkillCooldown{nil, {{ID: 2121003, ExpiresAt: now.Add(-time.Second)}}, {{ID: 2121003, ExpiresAt: now.Add(10 * time.Second)}}} {
		c := domain.Character{ID: 1, SkillCooldowns: []domain.SkillCooldown{existing}}
		ticket := &combatTicket{actorSession: domain.CharacterSession{CharacterID: 1}, generic: &genericAttack{}, request: domain.CombatAttackRequest{SkillID: 2121003, Cost: &domain.CombatCost{CooldownMS: 10000}}}
		mergeCommittedCombatCooldown(ticket, &c, domain.Character{ID: 1, SkillCooldowns: rows}, now)
		if len(c.SkillCooldowns) != 1 || c.SkillCooldowns[0] != existing || ticket.committedCooldown != nil {
			t.Fatal("partial/expired/older row overwrote current cooldown")
		}
	}
}
