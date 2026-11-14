package world

import (
	"context"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func taxiFixture(t *testing.T) (*worldScript, *departureRepository) {
	t.Helper()
	s, live, sink, _ := sceneFixture(t)
	s.scripts = nil
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, id := range []int{120000000, 102000000, 100000000, 103000000} {
		data := fmt.Sprintf(`<imgdir name="%09d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="10"/><int name="y" value="20"/></imgdir><imgdir name="6"><string name="pn" value="sp6"/><int name="pt" value="0"/><int name="x" value="60"/><int name="y" value="20"/></imgdir></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(root, "Map.wz/Map/Map1", fmt.Sprintf("%09d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	live.session.Character.MapID = domain.LithTaxiMap
	live.session.Character.Job = 0
	live.session.Character.Mesos = 1000
	s.gameplay.rooms[mapKey{channel: 1, mapID: domain.LithTaxiMap}] = &gameplayRoom{npcs: map[int32]content.Life{1: {TemplateID: domain.LithTaxiNPC}}}
	r := &departureRepository{}
	s.repo = r
	seedBoundScene(t, s, live, sink)
	return &worldScript{server: s, live: live, sink: sink, npcID: domain.LithTaxiNPC, objectID: 1}, r
}
func TestTaxiHostAtomicRequestAndPublication(t *testing.T) {
	for _, coupon := range []bool{false, true} {
		t.Run(fmt.Sprint(coupon), func(t *testing.T) {
			a, r := taxiFixture(t)
			choice, fare := int64(1), int64(100)
			if coupon {
				choice, fare = 2, 0
				a.live.session.Character.Items = []domain.InventoryItem{{ID: 99, TemplateID: domain.LithTaxiCoupon, InvType: 4, Slot: 1, Quantity: 1}}
			}
			plan, _ := domain.LithTaxiPlan(a.live.session.Character, int(choice), fare)
			dest, err := a.server.gameplay.catalog.Map(plan.MapID)
			if err != nil {
				t.Fatal(err)
			}
			p, err := taxiPortal(dest, "taxi-owned-command", coupon)
			if err != nil {
				t.Fatal(err)
			}
			result := a.live.session.Character
			result.MapID = plan.MapID
			result.SpawnPoint = p.ID
			result.X = p.X
			result.Y = p.Y
			result.Mesos -= fare
			result.Revision++
			if coupon {
				result.Items = nil
			}
			r.result = domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: result}}
			out, err := a.takeTaxi(context.Background(), scripting.Command{Script: "npc/1002007.lua", ID: "taxi-owned-command", Ints: []int64{choice, fare}})
			if err != nil || out.Value.(map[string]any)["ok"] != true || len(r.requests) != 1 {
				t.Fatal(out, err, r.requests)
			}
			q := r.requests[0]
			if q.MesosDelta != -fare || q.Travel == nil || q.Travel.Taxi == nil || q.Travel.Taxi.Choice != int(choice) || q.Travel.Taxi.DisplayedFare != fare {
				t.Fatal(q)
			}
			if coupon && (q.Travel.SpawnPoint != 6 || len(q.Items) != 1 || q.Items[0].Quantity != -1) {
				t.Fatal(q)
			}
			if a.live.session.Character.MapID != plan.MapID || a.sink.(*gameplayTestSink).count(protocol.SendSetField) != 1 {
				t.Fatal("committed trip unpublished")
			}
		})
	}
}
func TestTaxiHostRejectsChangedContextWithoutAssetCommand(t *testing.T) {
	for _, name := range []string{"npc", "script", "map", "far", "dead", "discount", "coupon", "choice", "trade"} {
		t.Run(name, func(t *testing.T) {
			a, r := taxiFixture(t)
			q := scripting.Command{Script: "npc/1002007.lua", ID: "taxi-denied-command", Ints: []int64{1, 100}}
			switch name {
			case "npc":
				a.npcID++
			case "script":
				q.Script = "npc/22000.lua"
			case "map":
				a.live.session.Character.MapID++
			case "far":
				a.live.session.Character.X = 1000
			case "dead":
				a.live.session.Character.HP = 0
			case "discount":
				a.live.session.Character.Job = 100
			case "coupon":
				a.live.session.Character.Items = []domain.InventoryItem{{TemplateID: domain.LithTaxiCoupon, Quantity: 1}}
			case "choice":
				q.Ints[0] = 4
			case "trade":
				a.server.trades = newTradeSpace()
				a.server.trades.rooms[a.live.session.CharacterID] = &tradeRoom{}
			}
			out, err := a.takeTaxi(context.Background(), q)
			if err == nil && out.Value.(map[string]any)["ok"] != false {
				t.Fatal(out)
			}
			if len(r.requests) != 0 {
				t.Fatal("charged refused taxi", r.requests)
			}
		})
	}
}

func TestTaxiRechecksContextAfterWaitingForPersistence(t *testing.T) {
	a, repo := taxiFixture(t)
	a.live.persistMu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		out, err := a.takeTaxi(context.Background(), scripting.Command{Script: "npc/1002007.lua", ID: "taxi-waiting-command", Ints: []int64{1, 100}})
		if err == nil && out.Value.(map[string]any)["ok"] != false {
			err = fmt.Errorf("changed source was accepted")
		}
		done <- err
	}()
	<-started
	a.live.mu.Lock()
	a.live.session.Character.MapID = 20000
	a.live.mu.Unlock()
	a.live.persistMu.Unlock()
	select {
	case err := <-done:
		if err != nil || len(repo.requests) != 0 {
			t.Fatal(err, repo.requests)
		}
	case <-time.After(time.Second):
		t.Fatal("taxi blocked after persistence was released")
	}
}
func TestTaxiPortalChoiceIsPinnedAndCouponExact(t *testing.T) {
	m := content.Map{Portals: []content.Portal{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 6}}}
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		op := fmt.Sprint(i)
		a, err := taxiPortal(m, op, false)
		b, e := taxiPortal(m, op, false)
		if err != nil || e != nil || a.ID != b.ID {
			t.Fatal(a, b, err, e)
		}
		seen[a.ID] = true
	}
	if len(seen) != 3 {
		t.Fatal("numeric random IDs and missing-index fallback differ", seen)
	}
	p, err := taxiPortal(m, "", true)
	if err != nil || p.ID != 6 {
		t.Fatal(p, err)
	}
	if _, err = taxiPortal(content.Map{Portals: []content.Portal{{ID: 0}}}, "x", true); err == nil {
		t.Fatal("coupon portal silently fell back")
	}
}
