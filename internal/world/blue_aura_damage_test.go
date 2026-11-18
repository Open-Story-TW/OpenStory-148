package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func TestV148BlueAuraContactSharesOneAcceptedHit(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		id, rank, recipientDamage, casterDamage int
		otherCast, outsideParty, rangedMobHit   bool
	}{
		{name: "basic", id: skills.BlueAuraSkill, rank: 20, recipientDamage: 70, casterDamage: 24},
		{name: "advanced", id: skills.AdvancedBlueAuraSkill, rank: 10, recipientDamage: 60, casterDamage: 36},
		{name: "different cast", id: skills.AdvancedBlueAuraSkill, rank: 10, recipientDamage: 100, otherCast: true},
		{name: "outside party", id: skills.AdvancedBlueAuraSkill, rank: 10, recipientDamage: 100, outsideParty: true},
		{name: "ranged mob attack", id: skills.AdvancedBlueAuraSkill, rank: 10, recipientDamage: 100, rangedMobHit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			s.skills, s.parties = newSkillSpace(), newPartySpace()
			mob.stats.PAD = 50
			expiry := time.Now().Add(time.Hour)
			party := domain.PartySnapshot{ID: 1, Revision: 1, Members: []domain.PartyMember{{CharacterID: 1}, {CharacterID: 2}}}
			for i, live := range lives {
				c := &live.session.Character
				c.HP, c.MaxHP = 200, 200
				c.X, c.Y = mob.life.X, mob.life.Y
				row := domain.SkillBuff{ID: tc.id, Level: tc.rank, Stat: "BLUE_AURA", Value: tc.rank, ExpiresAt: expiry, SourceCharacterID: 1}
				if i == 0 && tc.otherCast {
					row.ExpiresAt = row.ExpiresAt.Add(time.Second)
				}
				c.SkillBuffs = []domain.SkillBuff{row}
				seedBoundScene(t, s, live, sinks[i])
				if err := s.enterSkills(context.Background(), 1, live, sinks[i]); err != nil {
					t.Fatal(err)
				}
				if i == 0 || !tc.outsideParty {
					p := newPartyPeer(live, sinks[i], party)
					s.parties.peers[c.ID] = p
					t.Cleanup(p.halt)
				}
			}

			contact := protocol.PlayerDamage{Tick: 41, Type: protocol.MobContactDamage, Damage: 100, MonsterID: mob.stats.ID, ObjectID: mob.objectID}
			if tc.rangedMobHit {
				contact.Type = 0
			}
			for range 2 {
				if err := s.takeDamage(context.Background(), lives[1], sinks[1], contact); err != nil {
					t.Fatal(err)
				}
			}
			owner, victim := s.skillSession(lives[0]).Character, s.skillSession(lives[1]).Character
			if owner.HP != 200-tc.casterDamage || victim.HP != 200-tc.recipientDamage {
				t.Fatalf("caster/recipient HP = %d/%d", owner.HP, victim.HP)
			}
			if sinks[0].count(0x13b) != 1 {
				t.Fatal("foreign hit replayed or lost")
			}
		})
	}
}
