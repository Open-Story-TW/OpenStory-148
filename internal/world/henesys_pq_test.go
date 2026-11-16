package world

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type henesysTestRepo struct {
	gameplayTestRepo
	drops     []domain.ItemDrop
	consumed  map[int32]bool
	exchanges []domain.ScriptExchange
}

func (*henesysTestRepo) MoveInventory(context.Context, domain.InventoryMoveRequest) (domain.InventoryResult, error) {
	panic("unused")
}

func (*henesysTestRepo) PickupItem(context.Context, domain.ItemPickupRequest) (domain.InventoryResult, error) {
	panic("unused")
}

func (r *henesysTestRepo) ListItemDrops(context.Context, int, int, int) ([]domain.ItemDrop, error) {
	return r.drops, nil
}

func (r *henesysTestRepo) ConsumeEventDrop(_ context.Context, d domain.ItemDrop) (bool, error) {
	if r.consumed[d.ObjectID] {
		return false, nil
	}
	r.consumed[d.ObjectID] = true
	return true, nil
}

func (r *henesysTestRepo) ExpireEventDrops(context.Context, int, int, int) error {
	r.drops = nil
	return nil
}

func (*henesysTestRepo) ReadScriptLogs(context.Context, domain.CharacterSession, []domain.ScriptLogKey) ([]domain.ScriptLogValue, error) {
	panic("unused")
}

func (r *henesysTestRepo) ExecuteScriptExchange(_ context.Context, x domain.ScriptExchange) (domain.ScriptExchangeResult, error) {
	r.exchanges = append(r.exchanges, x)
	c := x.Session.Character
	c.Revision++
	c.EXP += x.EXP
	if len(x.Items) > 0 {
		c.Items = nil
	}
	return domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: c}}, nil
}

func moonPQFixture(t *testing.T, n int) (*eventFixture, *henesysTestRepo) {
	t.Helper()
	f := eventTestFixture(t)
	root := wzRoot()
	stage := `<imgdir name="life"><imgdir name="0"><string name="type" value="m"/><string name="id" value="9300062"/></imgdir></imgdir>
	<imgdir name="foothold"><imgdir name="0"><imgdir name="0"><imgdir name="123"><int name="x1" value="-500"/><int name="x2" value="600"/><int name="y1" value="-186"/><int name="y2" value="-186"/></imgdir></imgdir></imgdir></imgdir>`
	for _, id := range []int{content.HenesysPQMap, content.HenesysPQLobby, content.HenesysPQExit} {
		extra := ""
		if id == content.HenesysPQMap {
			extra = stage
		}
		doc := `<imgdir name="map"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir>` + extra + `</imgdir>`
		if err := os.WriteFile(filepath.Join(root, "Map.wz", "Map", "Map9", fmt.Sprintf("%d.img.xml", id)), []byte(doc), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int{content.MoonBunny, 9300062} {
		doc := `<imgdir name="mob"><imgdir name="info"><int name="maxHP" value="900"/><int name="PADamage" value="55"/><int name="dropItemPeriod" value="6"/></imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(doc), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := domain.PartySnapshot{ID: 1, LeaderID: 1}
	for i := 1; i <= n; i++ {
		p.Members = append(p.Members, domain.PartyMember{CharacterID: int64(i)})
	}
	for _, m := range p.Members {
		f.join(t, m.CharacterID, 50, content.HenesysPQLobby, p)
	}
	repo := &henesysTestRepo{gameplayTestRepo: gameplayTestRepo{save: func(domain.CharacterSession, domain.Character) error { return nil }}, consumed: map[int32]bool{}}
	f.server.repo = repo
	return f, repo
}

func TestHenesysPQThreeMembersPlantAndClear(t *testing.T) {
	small, _ := moonPQFixture(t, 2)
	ctx := context.Background()
	name := content.HenesysPQName
	if reason, _, _, err := small.server.eventStart(ctx, name, 1, small.lives[1], small.sinks[1]); err != nil || reason != "party_size" {
		t.Fatalf("two players: %s %v", reason, err)
	}

	f, repo := moonPQFixture(t, 3)
	s := f.server
	if reason, _, _, err := s.eventStart(ctx, name, 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
		t.Fatalf("start: %s %v", reason, err)
	}
	inst := s.eventOf(1)
	room := s.gameplay.rooms[mapKey{1, content.HenesysPQMap}]
	if room == nil || room.moon == nil || len(room.mobs) != 0 || len(room.reactors) != 7 {
		t.Fatal("escort or attackers were spawned before planting")
	}
	now := time.Now()
	for i, socket := range content.MoonFlowerReactors {
		repo.drops = append(repo.drops, domain.ItemDrop{ObjectID: int32(i + 1), OwnerID: int64(i%3 + 1),
			TemplateID: content.MoonFlowerSeed, Quantity: 1, PlayerDrop: true, X: socket.X, Y: socket.Y + 5})
	}
	repo.drops[5].PlayerDrop = false
	if err := s.advanceMoonRabbit(ctx, inst, now); err != nil {
		t.Fatal(err)
	}
	if room.moon.moon.state != 5 || len(repo.consumed) != 5 || len(room.mobs) != 0 {
		t.Fatal("a wild seed or duplicate activated the moon")
	}
	repo.drops[5].PlayerDrop = true
	if err := s.advanceMoonRabbit(ctx, inst, now); err != nil {
		t.Fatal(err)
	}
	if room.moon.moon.state != 6 || len(repo.consumed) != 6 || len(room.mobs) != 2 {
		t.Fatal("full moon must spawn the escort and enemies")
	}
	bunny, threat := room.moon.bunny, room.moon.attackers[0]
	threat.life.X, threat.life.Y = bunny.life.X, bunny.life.Y
	id := threat.controller
	hit := protocol.MobHitEscort{Attacker: threat.objectID, Escort: bunny.objectID, CharacterID: int32(id)}
	s.moonBunnyHit(1, f.lives[id], f.sinks[id], hit, now)
	s.moonBunnyHit(1, f.lives[id], f.sinks[id], hit, now.Add(time.Second))
	if bunny.hp != 845 {
		t.Fatalf("HP after two rapid hits = %d", bunny.hp)
	}

	if err := s.advanceMoonRabbit(ctx, inst, now.Add(11*time.Second)); err != nil || inst.moon.cakes != 0 {
		t.Fatalf("premature cake: %d %v", inst.moon.cakes, err)
	}
	for i := 0; i < moonRiceCakesToClear; i++ {
		if err := s.advanceMoonRabbit(ctx, inst, now.Add(time.Duration(12+i*6)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	leader := f.lives[1]
	if reason := s.finishMoonRabbit(ctx, inst, partySession(leader)); reason != "cakes" {
		t.Fatalf("clear without cakes: %s", reason)
	}
	leader.session.Character.Items = []domain.InventoryItem{{TemplateID: content.MoonRiceCake, Quantity: moonRiceCakesToClear}}
	if reason := s.finishMoonRabbit(ctx, inst, partySession(leader)); reason != "ok" {
		t.Fatalf("clear: %s", reason)
	}
	f.settle(t, func() bool {
		return f.mapOf(1) == content.HenesysPQExit && f.mapOf(2) == content.HenesysPQExit && f.mapOf(3) == content.HenesysPQExit
	})
	for i := int64(1); i <= 3; i++ {
		peer := partySession(f.lives[i]).Character
		if peer.EXP != henesysClearEXP(50) || len(peer.Items) != 0 {
			t.Fatalf("participant %d: exp=%d items=%v", i, peer.EXP, peer.Items)
		}
	}
}
