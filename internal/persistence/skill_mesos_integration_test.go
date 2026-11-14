package persistence

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestStagedSkillMesosSpawnConsumeReplayAndReject(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	a := actors[0]
	a.ChannelID = 1
	op := func(k string) string { return fmt.Sprintf("s0b-meso-%s-%d", k, time.Now().UnixNano()) }
	spawnOp := op("spawn")
	d1, err := s.SpawnSkillMesos(ctx, domain.SkillMesoSpawnRequest{Session: a, OperationID: spawnOp, SkillID: 4211003, X: 10, Y: 0, Amount: 120})
	if err != nil || d1.ObjectID == 0 || d1.Mesos != 120 || d1.OwnerID != a.CharacterID {
		t.Fatalf("spawn %+v %v", d1, err)
	}
	if again, err := s.SpawnSkillMesos(ctx, domain.SkillMesoSpawnRequest{Session: a, OperationID: spawnOp, SkillID: 4211003, X: 10, Y: 0, Amount: 120}); err != nil || again.ObjectID != d1.ObjectID {
		t.Fatalf("spawn replay %+v %v", again, err)
	}
	d2, err := s.SpawnSkillMesos(ctx, domain.SkillMesoSpawnRequest{Session: a, OperationID: op("spawn2"), SkillID: 4211003, X: 20, Y: 0, Amount: 30})
	if err != nil {
		t.Fatal(err)
	}
	consumeOp := op("consume")
	got, err := s.ConsumeMesoDrops(ctx, domain.MesoConsumeRequest{Session: a, OperationID: consumeOp, SkillID: 4211006, ObjectIDs: []int32{d2.ObjectID, d1.ObjectID}})
	if err != nil || len(got) != 2 || got[0].Mesos+got[1].Mesos != 150 {
		t.Fatalf("consume %+v %v", got, err)
	}
	if replay, err := s.ConsumeMesoDrops(ctx, domain.MesoConsumeRequest{Session: a, OperationID: consumeOp, SkillID: 4211006, ObjectIDs: []int32{d1.ObjectID, d2.ObjectID}}); err != nil || len(replay) != 2 {
		t.Fatalf("consume replay %+v %v", replay, err)
	}
	if _, err = s.ConsumeMesoDrops(ctx, domain.MesoConsumeRequest{Session: a, OperationID: op("again"), SkillID: 4211006, ObjectIDs: []int32{d1.ObjectID}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("consumed drop taken twice: %v", err)
	}
	var live int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE object_id IN ($1,$2) AND expired_at IS NULL`, d1.ObjectID, d2.ObjectID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("drops still live: %d %v", live, err)
	}
}

func TestStagedStealTakesOneItemPerSpawn(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	a := actors[0]
	a.ChannelID = 1
	spawn := fmt.Sprintf("mob-steal-test-%d", time.Now().UnixNano())
	op := "steal-" + spawn
	d, err := s.StealMobItem(ctx, domain.MobStealRequest{Session: a, OperationID: op, SkillID: 4201004, MobID: 100100, SpawnOperationID: spawn, X: 5, Y: 0})
	if err != nil || d == nil || d.TemplateID == 0 || d.InvType < 2 || d.OwnerID != a.CharacterID {
		t.Fatalf("steal %+v %v", d, err)
	}
	if again, err := s.StealMobItem(ctx, domain.MobStealRequest{Session: a, OperationID: op, SkillID: 4201004, MobID: 100100, SpawnOperationID: spawn, X: 5, Y: 0}); err != nil || again == nil || again.ObjectID != d.ObjectID {
		t.Fatalf("steal replay %+v %v", again, err)
	}
	if _, err = s.StealMobItem(ctx, domain.MobStealRequest{Session: a, OperationID: op + "-2", SkillID: 4201004, MobID: 100100, SpawnOperationID: spawn, X: 5, Y: 0}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("spawn stolen twice: %v", err)
	}
	boss := spawn + "-boss"
	if _, err = s.StealMobItem(ctx, domain.MobStealRequest{Session: a, OperationID: "steal-" + boss, SkillID: 4201004, MobID: 100100, SpawnOperationID: boss, X: 5, Y: 0, Boss: true}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("boss stolen from: %v", err)
	}
}
