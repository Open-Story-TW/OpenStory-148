package world

import (
	"bytes"
	"context"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

var skillEnds = make(chan int, 4)

func init() {
	skills.Register(skills.Handler{ID: 99990001, Kind: skills.KindBuff, OnEnd: func(c *skills.EndContext) { skillEnds <- c.SkillID }})
	skills.Register(skills.Handler{ID: 99990002, Kind: skills.KindBuff, DropOnEnter: func(c domain.Character) bool { return c.Job < 3200 || c.Job > 3212 }})
}

type skillChangeRepository struct {
	skillTestRepository
	changes chan []domain.SkillBuffChange
}

func (r skillChangeRepository) ChangeSkillBuffs(_ context.Context, session domain.CharacterSession, changes []domain.SkillBuffChange) (domain.SkillResult, error) {
	r.changes <- changes
	return domain.SkillResult{Character: session.Character}, nil
}

func TestSkillOnEndRunsWhenRowsEnd(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
	row := domain.SkillBuff{ID: 99990001, Level: 1, Stat: "WATK", Value: 5, ExpiresAt: time.Now().Add(time.Minute)}
	a := buffScopePlayer(1, 0, row)
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, a, sink)
	snapshot := cloneCombatSession(a.session)
	a.mu.Lock()
	a.session.Character.SkillBuffs = nil
	a.mu.Unlock()
	s.publishSkillTick(snapshot, sink, domain.SkillResult{Expired: []domain.SkillBuff{row}}, 50, snapshot.Character.MP)
	select {
	case id := <-skillEnds:
		if id != 99990001 {
			t.Fatal(id)
		}
	default:
		t.Fatal("OnEnd not called")
	}
}

func TestDropOnEnterCancelsRejectedHolderRows(t *testing.T) {
	changes := make(chan []domain.SkillBuffChange, 2)
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}, repo: skillChangeRepository{changes: changes}}
	row := domain.SkillBuff{ID: 99990002, Level: 3, Stat: "ELEMENTAL_STATUS_R", Value: 5, ExpiresAt: time.Now().Add(time.Hour)}
	a := buffScopePlayer(1, 0, row)
	enterBuffScope(t, s, a, &gameplayTestSink{})
	select {
	case got := <-changes:
		if len(got) != 1 || got[0].SkillID != 99990002 || got[0].Level != 3 || !got[0].Cancel {
			t.Fatalf("%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rejected holder kept the row")
	}
	b := buffScopePlayer(2, 20, row)
	b.session.Character.Job = 3212
	enterBuffScope(t, s, b, &gameplayTestSink{})
	select {
	case got := <-changes:
		t.Fatalf("caster job lost its row %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAppliedSkillRowsArePublishedForTheCast(t *testing.T) {
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
	a := buffScopePlayer(1, 0)
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, a, sink)
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		c := q.Session.Character
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: 35121013, Level: 10, Stat: "MECH_CHANGE", Value: 10, ExpiresAt: time.Now().Add(time.Hour)}}
		return domain.SkillResult{Character: c, Changed: true, Effect: true, Applied: 35121013}, nil
	}}
	if err := s.castSkill(context.Background(), a, sink, protocol.SkillCast{SkillID: 35111004, Level: 10, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if sink.count(0x2d) != 1 {
		t.Fatalf("applied rows not published: %d", sink.count(0x2d))
	}
}

func TestPiratesRevengePublishesAffectedEffectOnce(t *testing.T) {
	for _, tc := range []struct {
		id       int
		affected bool
	}{{5220012, true}, {5820011, false}} {
		s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}}
		owner, peer := buffScopePlayer(1, 0), buffScopePlayer(2, 30)
		local, remote := &gameplayTestSink{}, &gameplayTestSink{}
		enterBuffScope(t, s, owner, local)
		enterBuffScope(t, s, peer, remote)
		row := domain.SkillBuff{ID: tc.id, Level: 10, Stat: "PIRATES_REVENGE", Value: 15, ExpiresAt: time.Now().Add(time.Minute)}
		owner.mu.Lock()
		owner.session.Character.SkillBuffs = []domain.SkillBuff{row}
		snapshot := cloneCombatSession(owner.session)
		owner.mu.Unlock()
		member := s.skillMemberFor(snapshot, owner, local)
		changes := []domain.SkillBuffChange{{SkillID: tc.id, Level: 10}}
		s.publishSkillChanges(member, snapshot, domain.SkillResult{}, changes, nil, false)
		s.publishSkillChanges(member, snapshot, domain.SkillResult{}, changes, []domain.SkillBuff{row}, false)
		for _, v := range []struct {
			sink *gameplayTestSink
			id   int64
		}{{local, 0}, {remote, owner.session.CharacterID}} {
			want := protocol.SkillAffectedEffect(v.id, tc.id, 10)
			count := 0
			for _, p := range buffPackets(v.sink) {
				if len(want) != 0 && bytes.Equal(want, p) {
					count++
				}
			}
			if (tc.affected && count != 1) || (!tc.affected && count != 0) {
				t.Fatalf("skill%d actor%d emit affected %d times, want=%v", tc.id, v.id, count, tc.affected)
			}
		}
	}
}

func TestDismountCancelsRiding(t *testing.T) {
	changes := make(chan []domain.SkillBuffChange, 1)
	s := &Server{skills: newSkillSpace(), maps: newMapSpace(100), online: map[int64]domain.CharacterSession{}, repo: skillChangeRepository{changes: changes}}
	row := domain.SkillBuff{ID: 10001004, Level: 1, Stat: "MONSTER_RIDING", Value: 1, ExpiresAt: time.Now().Add(time.Hour)}
	a := buffScopePlayer(1, 0, row)
	enterBuffScope(t, s, a, &gameplayTestSink{})
	s.dismount(a.session.Character)
	select {
	case got := <-changes:
		if len(got) != 1 || got[0].SkillID != 10001004 || !got[0].Cancel {
			t.Fatalf("%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("riding kept after unequipping the mount")
	}
}
