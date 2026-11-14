package world

import (
	"context"
	"errors"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
	"testing"
	"time"
)

type secondJobWorldRepo struct {
	domain.Repository
	calls, resolves int
	execute         func(domain.JobAdvancementRequest) (domain.JobAdvancementResult, error)
	resolve         func(domain.JobAdvancementRequest) (domain.JobAdvancementResult, bool, error)
}

func (r *secondJobWorldRepo) AdvanceSecondJob(_ context.Context, q domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	r.calls++
	return r.execute(q)
}
func (r *secondJobWorldRepo) ResolveSecondJob(_ context.Context, q domain.JobAdvancementRequest) (domain.JobAdvancementResult, bool, error) {
	r.resolves++
	return r.resolve(q)
}
func (r *secondJobWorldRepo) ValidateSceneOwner(_ context.Context, q domain.CharacterSession) error {
	if q.Generation != 2 {
		return domain.ErrStaleOwner
	}
	return nil
}
func secondJobHostFixture(t *testing.T) (*Server, *worldScript, *secondJobWorldRepo, *gameplayTestSink) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	r := &secondJobWorldRepo{}
	live := &liveCharacter{session: domain.CharacterSession{AccountID: 2, CharacterID: 1, SessionID: "spearman-world-owned-session", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, AccountID: 2, Job: 100, Level: 30, MapID: 103000003, X: 78, Y: 137, HP: 100, MP: 20, MaxHP: 500, MaxMP: 100, BaseMaxHP: 500, BaseMaxMP: 100, VitalsModel: 1, Revision: 1, SP: 5}}}
	sink := &gameplayTestSink{}
	s := &Server{repo: r, gameplay: g, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{}}
	g.rooms[mapKey{1, 103000003}] = &gameplayRoom{npcs: map[int32]content.Life{50: {TemplateID: 1052001, X: 78, Y: 137}}, members: map[int64]*gameplayMember{1: {live: live, sink: sink}}}
	seedBoundScene(t, s, live, sink)
	s.maps.Enter(1, 103000003, live.session.Character, sink)
	a := &worldScript{server: s, live: live, sink: sink, npcID: 1052001, objectID: 50, identity: scripting.Identity{CharacterID: 1, SessionID: live.session.SessionID, Generation: 1}}
	return s, a, r, sink
}
func secondJobCommand() scripting.Command {
	return scripting.Command{Kind: "advance_second_job", Script: "npc/1052001.lua", ID: "spearman-world-same-operation", Ints: []int64{130}}
}
func committedSecondJob(q domain.JobAdvancementRequest) domain.JobAdvancementResult {
	c := q.Session.Character
	c.Job = 130
	c.SP++
	c.Revision++
	return domain.JobAdvancementResult{InventoryResult: domain.InventoryResult{Character: c}, FromJob: 100, ToJob: 130}
}
func TestSecondJobUnknownCommitResolvesOriginalOperationOnce(t *testing.T) {
	_, a, r, sink := secondJobHostFixture(t)
	var committed domain.JobAdvancementResult
	var operation string
	r.execute = func(q domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
		operation = q.OperationID
		committed = committedSecondJob(q)
		return domain.JobAdvancementResult{}, errors.New("commit reply lost")
	}
	r.resolve = func(q domain.JobAdvancementRequest) (domain.JobAdvancementResult, bool, error) {
		if q.OperationID != operation {
			t.Fatal("resolve rebound operation")
		}
		return committed, true, nil
	}
	before := packetCount(sink)
	result, err := a.Execute(context.Background(), a.identity, secondJobCommand())
	if err != nil || result.Value.(map[string]any)["ok"] != true || r.calls != 1 || r.resolves != 1 || a.live.session.Character.Job != 130 || a.live.session.Character.SP != 6 || packetCount(sink) != before+1 {
		t.Fatalf("resolution failed %+v %v calls%d/%d", result, err, r.calls, r.resolves)
	}
}
func TestSecondJobStaleResolutionDoesNotPublish(t *testing.T) {
	_, a, r, sink := secondJobHostFixture(t)
	r.execute = func(q domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
		return domain.JobAdvancementResult{}, errors.New("unknown commit")
	}
	r.resolve = func(q domain.JobAdvancementRequest) (domain.JobAdvancementResult, bool, error) {
		return domain.JobAdvancementResult{}, true, domain.ErrStaleOwner
	}
	before := packetCount(sink)
	_, err := a.Execute(context.Background(), a.identity, secondJobCommand())
	if !errors.Is(err, domain.ErrStaleOwner) || r.calls != 1 || r.resolves != 1 || a.live.session.Character.Job != 100 || packetCount(sink) != before {
		t.Fatal("stale resolve published", err)
	}
}
func TestSecondJobCommittedResultCannotOutliveReplacedScene(t *testing.T) {
	s, a, r, sink := secondJobHostFixture(t)
	committed, release := make(chan struct{}), make(chan struct{})
	r.execute = func(q domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
		v := committedSecondJob(q)
		close(committed)
		<-release
		return v, nil
	}
	done := make(chan error, 1)
	go func() { _, err := a.Execute(context.Background(), a.identity, secondJobCommand()); done <- err }()
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("advance did not commit")
	}
	replacement := &liveCharacter{session: a.live.session}
	replacement.session.Generation = 2
	replacement.session.Character.MapID = 40000
	newSink := &gameplayTestSink{}
	if err := s.publishInitialScene(context.Background(), replacement, newSink, 1); err != nil {
		t.Fatal(err)
	}
	beforeOld, beforeNew := packetCount(sink), packetCount(newSink)
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrStaleOwner) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("old advancement stuck")
	}
	if a.live.session.Character.Job != 100 || packetCount(sink) != beforeOld || packetCount(newSink) != beforeNew || s.sceneFor(replacement, newSink) == nil {
		t.Fatal("late advancement overwrote replacement")
	}
}
