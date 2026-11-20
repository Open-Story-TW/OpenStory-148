package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type relogClearRepo struct {
	domain.Repository
	calls int
}

func (r *relogClearRepo) ClearSkillBuffs(_ context.Context, session domain.CharacterSession) (domain.SkillResult, error) {
	r.calls++
	c := session.Character
	c.SkillBuffs = nil
	c.Revision++
	return domain.SkillResult{Character: c, Changed: true}, nil
}

func TestDropRelogBuffsClearsLiveRows(t *testing.T) {
	repo := &relogClearRepo{}
	s := &Server{repo: repo}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, HP: 50, Revision: 3,
		SkillBuffs: []domain.SkillBuff{{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}}}}}
	s.dropRelogBuffs(context.Background(), live)
	if repo.calls != 1 || len(live.session.Character.SkillBuffs) != 0 || live.session.Character.Revision != 4 {
		t.Fatalf("calls %d, buffs %v, revision %d", repo.calls, live.session.Character.SkillBuffs, live.session.Character.Revision)
	}
	// Nothing saved: no transaction.
	s.dropRelogBuffs(context.Background(), live)
	if repo.calls != 1 {
		t.Fatal("cleared a character without buffs")
	}
}

func TestRestoreSummonsUsesRemainingBuffTime(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	now := time.Now()
	lives[0].session.Character.SkillBuffs = []domain.SkillBuff{
		{ID: testSummonSkill, Level: 2, Stat: "SUMMON", Value: 1, ExpiresAt: now.Add(4 * time.Second)},
		{ID: sceneLocalSG88, Level: 30, Stat: "COMBO", Value: 1, ExpiresAt: now.Add(time.Hour)},     // legacy orphan
		{ID: testPuppetSkill, Level: 1, Stat: "PUPPET", Value: 1, ExpiresAt: now.Add(-time.Second)}, // expired
		{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: now.Add(time.Minute)},           // not a summon
	}
	s.restoreSummons(1, lives[0])
	if sinks[0].count(protocol.SendSpawnSummon) != 1 || sinks[1].count(protocol.SendSpawnSummon) != 1 {
		t.Fatal("restored summon not shown to owner and peer")
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	room := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}]
	if len(room.summons) != 1 {
		t.Fatalf("%d summons restored, want 1", len(room.summons))
	}
	for _, m := range room.summons {
		if m.source != testSummonSkill || m.level != 2 {
			t.Fatalf("restored skill %d level %d", m.source, m.level)
		}
		// Spec duration is 10s; the buff has 4s left.
		if left := m.expiresAt.Sub(now); left > 4*time.Second || left < 3*time.Second {
			t.Fatalf("summon lives %v, want the buff's 4s", left)
		}
	}
}

func TestEnteringFieldRetiresOnlyLegacySG88MarkerWithoutRestoringIt(t *testing.T) {
	changes := make(chan []domain.SkillBuffChange, 1)
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}, repo: skillChangeRepository{changes: changes}}
	expiry := time.Now().Add(time.Hour)
	live := buffScopePlayer(1, 0,
		domain.SkillBuff{ID: sceneLocalSG88, Level: 30, Stat: "COMBO", Value: 1, ExpiresAt: expiry},
		domain.SkillBuff{ID: 1001003, Level: 10, Stat: "WDEF", Value: 20, ExpiresAt: expiry})
	live.session.Character.Job = 3512
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, live, sink)
	if actual := sink.count(0x2d); actual != 1 {
		t.Fatalf("restored %d local buff packets; expected only the unrelated active buff", actual)
	}
	select {
	case pending := <-changes:
		if len(pending) != 1 || pending[0].SkillID != sceneLocalSG88 || !pending[0].Cancel {
			t.Fatalf("unexpected field entry skill mutation: %+v", pending)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy SG-88 marker was not retired on field entry")
	}
}

func TestApplyItemBuffInstallsRowsAndShowsIcon(t *testing.T) {
	s := &Server{online: map[int64]domain.CharacterSession{}}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, HP: 50}}}
	sink := &gameplayTestSink{}
	row := domain.SkillBuff{ID: -2002000, Level: 1, Stat: "AVOID", Value: 100, ExpiresAt: time.Now().Add(3 * time.Minute)}
	s.applyItemBuff(live, sink, domain.ItemUseResult{Character: domain.Character{ID: 1, SkillBuffs: []domain.SkillBuff{row}}, Buffs: []domain.SkillBuff{row}})
	if len(live.session.Character.SkillBuffs) != 1 || live.session.Character.SkillBuffs[0].ID != -2002000 {
		t.Fatalf("live rows %+v", live.session.Character.SkillBuffs)
	}
	if sink.count(0x2d) != 1 {
		t.Fatal("item buff icon not sent")
	}
}
