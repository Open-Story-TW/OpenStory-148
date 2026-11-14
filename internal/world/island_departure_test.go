package world

import (
	"context"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type departureRepository struct {
	gameplayTestRepo
	requests []domain.ScriptExchange
	result   domain.ScriptExchangeResult
}

func (r *departureRepository) ReadScriptLogs(context.Context, domain.CharacterSession, []domain.ScriptLogKey) ([]domain.ScriptLogValue, error) {
	return nil, nil
}
func (r *departureRepository) ExecuteScriptExchange(_ context.Context, q domain.ScriptExchange) (domain.ScriptExchangeResult, error) {
	r.requests = append(r.requests, q)
	return r.result, nil
}

func TestIslandDepartureSelectsOneAtomicFareOrRecommendation(t *testing.T) {
	for _, letter := range []bool{false, true} {
		t.Run(map[bool]string{false: "fare", true: "recommendation"}[letter], func(t *testing.T) {
			s, live, sink, _ := sceneFixture(t)
			s.scripts = nil
			live.session.Character.MapID = 2000000
			live.session.Character.Level = 5
			live.session.Character.Mesos = 1000
			if letter {
				live.session.Character.Level = 1
				live.session.Character.Items = []domain.InventoryItem{{ID: 9, TemplateID: 4031801, InvType: 4, Slot: 1, Quantity: 1}}
			}
			s.gameplay.rooms[mapKey{channel: 1, mapID: 2000000}] = &gameplayRoom{npcs: map[int32]content.Life{1: {TemplateID: 22000, X: 0, Y: 0}}}
			result := live.session.Character
			result.MapID = 2010000
			result.Revision++
			result.SpawnPoint = 0
			result.X = 0
			result.Y = 0
			if letter {
				result.Items = nil
			} else {
				result.Mesos = 850
			}
			repo := &departureRepository{result: domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: result}}}
			s.repo = repo
			seedBoundScene(t, s, live, sink)
			a := &worldScript{server: s, live: live, sink: sink, npcID: 22000, objectID: 1}
			out, err := a.departIsland(context.Background(), scripting.Command{Script: "npc/22000.lua", ID: "island-departure-owned-command"})
			if err != nil || out.Value.(map[string]any)["ok"] != true {
				t.Fatal(out, err)
			}
			if len(repo.requests) != 1 {
				t.Fatal("departure made multiple asset commands")
			}
			q := repo.requests[0]
			if q.Travel == nil || q.Travel.FromMap != 2000000 || q.Travel.MapID != 2010000 || q.Travel.SpawnPoint != 0 {
				t.Fatalf("travel %+v", q.Travel)
			}
			if letter {
				if q.MesosDelta != 0 || len(q.Items) != 1 || q.Items[0].TemplateID != 4031801 || q.Items[0].Quantity != -1 {
					t.Fatalf("recommendation %+v", q)
				}
			} else if q.MesosDelta != -150 || len(q.Items) != 0 {
				t.Fatalf("fare %+v", q)
			}
			if live.session.Character.MapID != 2010000 || sink.count(protocol.SendSetField) != 1 {
				t.Fatal("committed destination not published")
			}
		})
	}
}

func TestIslandDepartureDeniesLowLevelWithoutCharging(t *testing.T) {
	s, live, sink, _ := sceneFixture(t)
	live.session.Character.MapID = 2000000
	live.session.Character.Level = 4
	s.gameplay.rooms[mapKey{channel: 1, mapID: 2000000}] = &gameplayRoom{npcs: map[int32]content.Life{1: {TemplateID: 22000}}}
	repo := &departureRepository{}
	s.repo = repo
	seedBoundScene(t, s, live, sink)
	a := &worldScript{server: s, live: live, sink: sink, npcID: 22000, objectID: 1}
	out, err := a.departIsland(context.Background(), scripting.Command{Script: "npc/22000.lua", ID: "island-denied-command"})
	if err != nil || out.Value.(map[string]any)["ok"] != false || len(repo.requests) != 0 {
		t.Fatal(out, err, repo.requests)
	}
}
