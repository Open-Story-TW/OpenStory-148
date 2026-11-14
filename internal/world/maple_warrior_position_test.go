package world

import (
	"context"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
)

func TestMapleWarriorUsesServerPositionDespiteStaleMassSpellXY(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	c := &lives[0].session.Character
	c.Job = 212
	c.Level = 150
	c.MP = 100
	c.MaxMP = 100
	c.BaseMaxMP = 100
	c.Skills = []domain.SkillState{{ID: 2121000, Level: 30, MasterLevel: 30}}
	c.X = 1111
	c.Y = 587
	lives[1].session.Character.X = 1500
	lives[1].session.Character.Y = 587
	calls := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		if q.Session.Character.X != 1111 || len(q.Members) != 2 {
			t.Fatalf("untrusted XY selected recipients: %+v", q)
		}
		return fakePartyBuffResult(t, q), nil
	}
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{SkillID: 2121000, Level: 30, Tick: 221, HasPosition: true, X: 1034, Y: 587}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || lives[0].session.Character.MP != 30 || len(lives[1].session.Character.SkillBuffs) != 1 || lives[1].session.Character.SkillBuffs[0].Value != 15 {
		t.Fatalf("not applied once: %d %+v", calls, lives[0].session.Character)
	}
}

func TestMapleWarriorPositionFixKeepsOtherPartyPositionGuard(t *testing.T) {
	s, lives, sinks, repo := partyBuffWorldFixture(t)
	calls := 0
	repo.apply = func(q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
		calls++
		return fakePartyBuffResult(t, q), nil
	}
	c := lives[0].session.Character
	if e := s.castSkill(context.Background(), lives[0], sinks[0], protocol.SkillCast{SkillID: 1301007, Level: 20, Tick: 222, HasPosition: true, X: int16(c.X + 77), Y: int16(c.Y)}); e != nil {
		t.Fatal(e)
	}
	if calls != 0 || lives[0].session.Character.MP != c.MP || len(lives[0].session.Character.SkillBuffs) != 0 {
		t.Fatal("unrelated party buff guard changed")
	}
}
