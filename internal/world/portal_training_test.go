package world

import (
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type trainingPortalRepository struct {
	gameplayTestRepo
	states  []domain.QuestState
	queried int64
	reads   int
}

func (r *trainingPortalRepository) QuestStates(_ context.Context, s domain.CharacterSession) ([]domain.QuestState, error) {
	r.queried = s.CharacterID
	r.reads++
	return r.states, nil
}
func (r *trainingPortalRepository) TransitionQuest(context.Context, domain.QuestRequest) (domain.QuestResult, error) {
	return domain.QuestResult{}, domain.ErrForbidden
}

func trainingHost(t *testing.T) (*portalScriptHost, *trainingPortalRepository) {
	t.Helper()
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	m, err := g.catalog.Map(1010000)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := m.Portal("in00")
	if !ok {
		t.Fatal("missing WZtraining portal")
	}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, AccountID: 7, SessionID: "training-owned-session", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 7, MapID: m.ID, HP: 50, MaxHP: 50, Level: 5, X: p.X, Y: p.Y}}}
	repo := &trainingPortalRepository{}
	s := &Server{gameplay: g, repo: repo, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{}}
	id := scripting.Identity{SessionID: live.session.SessionID, CharacterID: 7, Generation: 3}
	sink := &gameplayTestSink{}
	seedBoundScene(t, s, live, sink)
	return &portalScriptHost{server: s, live: live, sink: sink, identity: id, channel: 1, mapID: m.ID, portal: p}, repo
}
func TestTrainingQuestQueryAndWarpRevalidateOwnedCurrentPortal(t *testing.T) {
	h, repo := trainingHost(t)
	repo.states = []domain.QuestState{{ID: 1041, State: 1}}
	q := scripting.Command{Kind: "quest_state", Script: "portal/entertraining.lua", Ints: []int64{1041}}
	got, err := h.Execute(context.Background(), h.identity, q)
	if err != nil || got.Value != int64(1) || repo.queried != 7 {
		t.Fatal(got, err)
	}
	for _, change := range []func(*portalScriptHost, *scripting.Command){
		func(h *portalScriptHost, q *scripting.Command) { h.live.session.Generation++ },
		func(h *portalScriptHost, q *scripting.Command) { h.live.session.Character.X += 1000 },
		func(h *portalScriptHost, q *scripting.Command) { h.live.session.Character.HP = 0 },
		func(h *portalScriptHost, q *scripting.Command) { h.live.session.ChannelID = 2 },
		func(h *portalScriptHost, q *scripting.Command) { h.live.session.Character.MapID = 10000 },
		func(h *portalScriptHost, q *scripting.Command) { h.portal.ID = 999 },
		func(h *portalScriptHost, q *scripting.Command) { q.Script = "portal/bigban.lua" },
		func(h *portalScriptHost, q *scripting.Command) { q.Ints = []int64{9999} },
		func(h *portalScriptHost, q *scripting.Command) { q.Ints = []int64{1041, 8} },
	} {
		h, r := trainingHost(t)
		copy := q
		change(h, &copy)
		if _, err := h.Execute(context.Background(), h.identity, copy); err == nil || r.reads != 0 {
			t.Fatal("unauthorized query reached repository", copy, err)
		}
	}
	// Forfeiting/completing between a debugger's query and effect cannot warp.
	repo.states = []domain.QuestState{{ID: 1041, State: 2}}
	if _, err = h.Execute(context.Background(), h.identity, scripting.Command{Kind: "warp", Script: q.Script, Ints: []int64{1010100, 4}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("stale eligibility warp", err)
	}
	if _, err = h.Execute(context.Background(), h.identity, scripting.Command{Kind: "open_shop", Script: q.Script, Ints: []int64{1}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("portal gained shop capability", err)
	}
	if _, err = h.Execute(context.Background(), h.identity, scripting.Command{Kind: "portal_message", Script: q.Script, Text: trainingPortalDenied}); err != nil {
		t.Fatal(err)
	}
	if h.sink.(*gameplayTestSink).count(0x58) != 1 {
		t.Fatal("denial feedback missing")
	}
}

func TestTrainingLuaCompletesFourRealMapTransitions(t *testing.T) {
	for q := 1041; q <= 1044; q++ {
		h, repo := trainingHost(t)
		repo.states = []domain.QuestState{{ID: q, State: 1}}
		var saved domain.Character
		repo.save = func(_ domain.CharacterSession, c domain.Character) error { saved = c; return nil }
		scripts, err := newScriptWorld()
		if err != nil {
			t.Fatal(err)
		}
		h.server.scripts = scripts
		if err = h.server.runPortalScript(context.Background(), 1, h.live, h.sink, h.portal); err != nil {
			t.Fatal(err)
		}
		if saved.MapID != 1010100+(q-1041)*100 || saved.SpawnPoint != 4 || h.live.session.Character.MapID != saved.MapID || scripts.active[7] != nil {
			t.Fatalf("quest%d saved%+v", q, saved)
		}
	}
}

func TestNativeSpecialPortalPacketReachesTrainingLua(t *testing.T) {
	for q := 1041; q <= 1044; q++ {
		h, repo := trainingHost(t)
		repo.states = []domain.QuestState{{ID: q, State: 1}}
		var saved domain.Character
		repo.save = func(_ domain.CharacterSession, c domain.Character) error { saved = c; return nil }
		scripts, err := newScriptWorld()
		if err != nil {
			t.Fatal(err)
		}
		h.server.scripts = scripts
		w := protocol.NewWriter(protocol.RecvScriptPortal)
		w.U8(34)
		w.String("in00")
		w.I16(70)
		w.I16(155)
		handled, err := h.server.handleGameplay(context.Background(), 1, h.live, h.sink, protocol.RecvScriptPortal, protocol.NewReader(w.Packet()[2:]))
		if !handled || err != nil || saved.MapID != 1010100+(q-1041)*100 || saved.SpawnPoint != 4 || h.sink.(*gameplayTestSink).count(protocol.SendSetField) != 1 {
			t.Fatalf("native quest %d route: handled=%v saved=%+v err=%v", q, handled, saved, err)
		}
	}
}

func TestSpecialPortalRejectsUnownedLocationOrUnscriptedPortal(t *testing.T) {
	for _, invalid := range []string{"far", "dead", "missing", "ordinary"} {
		t.Run(invalid, func(t *testing.T) {
			h, repo := trainingHost(t)
			repo.states = []domain.QuestState{{ID: 1041, State: 1}}
			req := protocol.ScriptPortalRequest{Name: "in00", X: 74, Y: 154}
			switch invalid {
			case "far":
				h.live.session.Character.X += 1000
			case "dead":
				h.live.session.Character.HP = 0
			case "missing":
				req.Name = "invented"
			case "ordinary":
				req.Name = "east00"
				h.live.session.Character.X = 683
				h.live.session.Character.Y = 211
			}
			if err := h.server.scriptPortalGameplay(context.Background(), 1, h.live, h.sink, req); err != nil {
				t.Fatal(err)
			}
			if repo.reads != 0 || h.sink.(*gameplayTestSink).count(protocol.SendSetField) != 0 || h.sink.(*gameplayTestSink).count(protocol.SendUpdateStats) != 1 {
				t.Fatal("invalid special portal reached quest or map mutation")
			}
		})
	}
}
