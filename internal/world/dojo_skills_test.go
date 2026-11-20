package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestInvincibleBarrierBlocksTakenDamage(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{skills: newSkillSpace(), gameplay: g}
	now := time.Now()
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, HP: 50, MaxHP: 50, MaxMP: 5, Revision: 1,
		SkillBuffs: []domain.SkillBuff{{ID: 1010, Level: 1, Stat: "DIVINE_BODY", Value: 1, ExpiresAt: now.Add(30 * time.Second)}}}}}
	sink := &gameplayTestSink{}
	if err = s.enterGameplay(1, a, sink); err != nil {
		t.Fatal(err)
	}
	_ = s.enterSkills(context.Background(), 1, a, sink)
	var mob *gameplayMob
	for _, m := range g.rooms[mapKey{channel: 1, mapID: 40000}].mobs {
		mob = m
		break
	}
	if mob == nil {
		t.Fatal("missing actual WZ mob")
	}
	a.session.Character.X, a.session.Character.Y = mob.life.X, mob.life.Y
	q := protocol.PlayerDamage{Tick: 1, Type: -1, Damage: 1, MonsterID: mob.stats.ID, ObjectID: mob.objectID}
	_ = s.takeDamage(context.Background(), a, sink, q)
	if a.session.Character.HP != 50 {
		t.Fatalf("invincible character took damage: HP %d", a.session.Character.HP)
	}
	a.session.Character.SkillBuffs = nil
	s.skills.members[1].lastDamage = now.Add(-2 * time.Second)
	q.Tick++
	_ = s.takeDamage(context.Background(), a, sink, q)
	if a.session.Character.HP != 49 {
		t.Fatalf("after the barrier: HP %d", a.session.Character.HP)
	}
}
