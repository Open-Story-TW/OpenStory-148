package world

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestInfinityTickPublishesValueAndMPOnlyRecoveryToCurrentVisit(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100)}
	buff := domain.SkillBuff{ID: 2121004, Level: 30, Stat: "INFINITY", Value: 11, ExpiresAt: time.Now().Add(time.Minute)}
	a, b := buffScopePlayer(1, 0, buff), buffScopePlayer(2, 20)
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	enterBuffScope(t, s, a, first)
	enterBuffScope(t, s, b, second)
	snap := cloneCombatSession(a.session)
	a.session.Character.MP = 29
	beforeLocal, beforeRemote, beforeHP := first.count(0x2d), second.count(0x145), first.count(0x2c)
	s.publishSkillTick(snap, first, domain.SkillResult{Character: a.session.Character, Updated: []domain.SkillBuff{buff}}, 50)
	if first.count(0x2d) != beforeLocal+1 || second.count(0x145) != beforeRemote+1 || first.count(0x2c) != beforeHP+1 {
		t.Fatal("missing value or MP-only update")
	}
	// The same delayed SQL result cannot publish to a replacement visit.
	s.leaveSkills(1, a)
	a.session.Generation++
	newSink := &gameplayTestSink{}
	enterBuffScope(t, s, a, newSink)
	beforeRemote = second.count(0x145)
	s.publishSkillTick(snap, first, domain.SkillResult{Updated: []domain.SkillBuff{buff}}, 50)
	if second.count(0x145) != beforeRemote || newSink.count(0x2c) != 0 {
		t.Fatal("old tick crossed visit")
	}
}
