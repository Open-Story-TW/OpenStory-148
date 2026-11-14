package persistence

import (
	"context"
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"sync"
	"testing"
)

func questDropFixture(t *testing.T, both bool) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	ctx, s, actors := inventoryFixture(t)
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for i := range actors {
		if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET map_id=40000,x=0,y=0,level=10 WHERE id=$1`, actors[i].CharacterID); err != nil {
			t.Fatal(err)
		}
		var err error
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 || both {
			v, err := s.TransitionQuest(ctx, domain.QuestRequest{Session: actors[i], QuestID: 1035, NPCID: 2004, Action: "start", OperationID: fmt.Sprintf("questdrop-start-%d", actors[i].CharacterID)})
			if err != nil {
				t.Fatal(err)
			}
			actors[i].Character = v.Character
		}
	}
	return ctx, s, actors
}

func killForQuestDrop(t *testing.T, ctx context.Context, s *Store, a *domain.CharacterSession, suffix string, witnesses ...*domain.CharacterSession) []domain.ItemDrop {
	t.Helper()
	op := fmt.Sprintf("questdrop-kill-%d-%s", a.CharacterID, suffix)
	parties, err := s.CombatParties(ctx, a.Lease, []int64{a.CharacterID})
	if err != nil {
		t.Fatal(err)
	}
	if len(parties) == 0 {
		v, e := s.AwardCombatKillAt(ctx, *a, op, 3, 9300018, 0, 0)
		if e != nil {
			t.Fatal(e)
		}
		a.Character = v
	} else {
		catalog, e := content.DefaultCatalog()
		if e != nil {
			t.Fatal(e)
		}
		mob, e := catalog.Mob(9300018)
		if e != nil {
			t.Fatal(e)
		}
		q := domain.CombatAttackRequest{Session: *a, OperationID: op + "-attack", ClientTick: uint32(a.Character.Revision), Family: domain.CombatNormal, Members: []domain.CharacterSession{*a}, Parties: parties, Targets: []domain.CombatTarget{{ObjectID: 1000001, MobID: 9300018, SpawnOperationID: op, BeforeHP: 1, Damage: 1, Defeated: true, Contributions: []domain.CombatContribution{{CharacterID: a.CharacterID, SessionID: a.SessionID, Generation: a.Generation, Damage: mob.HP}}}}}
		for _, w := range witnesses {
			q.Members = append(q.Members, *w)
		}
		v, e := s.ApplyCombatAttack(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range v.Characters {
			if c.ID == a.CharacterID {
				a.Character = c
			}
			for _, w := range witnesses {
				if w.CharacterID == c.ID {
					w.Character = c
				}
			}
		}
	}
	drops, err := s.MaterializeCombatLoot(ctx, a.Lease, op)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.MaterializeCombatLoot(ctx, a.Lease, op); err != nil || len(again) != 0 {
		t.Fatal("loot job replay duplicated objects")
	}
	return drops
}

func TestQuestDropKillPickupReplayNeedAndOwner(t *testing.T) {
	ctx, s, actors := questDropFixture(t, false)
	drops := killForQuestDrop(t, ctx, s, &actors[0], "first")
	if len(drops) != 1 || drops[0].TemplateID != 4031802 || drops[0].Quantity != 1 || drops[0].QuestID != 1035 || drops[0].QuestDropType != 0 || drops[0].QuestOwnerUntil <= 0 {
		t.Fatalf("two source rows exceeded need1 or lost quest scope: %+v", drops)
	}
	d := drops[0]
	reload, err := s.ListItemDrops(ctx, 124, 1, 40000)
	if err != nil || len(reload) != 1 || reload[0].QuestID != 1035 || reload[0].QuestOwnerUntil != d.QuestOwnerUntil {
		t.Fatalf("drop scope roundtrip: %+v %v", reload, err)
	}
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("inactive peer picked quest drop: %v", err)
	}
	// Expired ownership never bypasses the quest-state requirement.
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_drops SET quest_owner_until=clock_timestamp()-interval '1 second' WHERE object_id=$1`, d.ObjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("FFA exposed quest item to nonquest peer")
	}
	v, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[0], ObjectID: d.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	actors[0].Character = v.Character
	replay, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[0], ObjectID: d.ObjectID})
	if err != nil || !replay.Replayed || len(replay.Changes) != 0 {
		t.Fatalf("pickup replay: %+v %v", replay, err)
	}
	if next := killForQuestDrop(t, ctx, s, &actors[0], "already-collected"); len(next) != 0 {
		t.Fatalf("needed count not checked at kill: %+v", next)
	}
	owned := 0
	for _, item := range actors[0].Character.Items {
		if item.TemplateID == 4031802 {
			owned += item.Quantity
		}
	}
	if owned != 1 {
		t.Fatalf("quest reward repeated: %d", owned)
	}
}

func TestQuestDropForfeitCapacityExpiryAndConcurrentEligiblePickup(t *testing.T) {
	ctx, s, actors := questDropFixture(t, true)
	d := killForQuestDrop(t, ctx, s, &actors[0], "capacity")[0]
	// An eligible nonparty player sees the object but cannot steal it early.
	if _, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner protection ignored: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) SELECT $1,4,n,4000019,100 FROM generate_series(1,24)n`, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actors[0].Character, err = s.GetCharacter(ctx, actors[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[0], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full ETC bag accepted: %v", err)
	}
	var picked bool
	if err = s.db.QueryRowContext(ctx, `SELECT picked_up_by IS NOT NULL FROM asset_item_drops WHERE object_id=$1`, d.ObjectID).Scan(&picked); err != nil || picked {
		t.Fatal("failed pickup removed durable object")
	}
	forfeited, err := s.TransitionQuest(ctx, domain.QuestRequest{Session: actors[0], QuestID: 1035, Action: "forfeit", OperationID: fmt.Sprintf("questdrop-forfeit-%d", actors[0].CharacterID)})
	if err != nil {
		t.Fatal(err)
	}
	actors[0].Character = forfeited.Character
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[0], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forfeited quest could pickup: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_drops SET quest_owner_until=clock_timestamp()-interval '1 second' WHERE object_id=$1`, d.ObjectID); err != nil {
		t.Fatal(err)
	}
	// Another active quest owner may now collect it; two connections of that
	// owner racing the same object must grant one item and one replay at most.
	var wg sync.WaitGroup
	wins := make(chan domain.InventoryResult, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: d.ObjectID})
			if e == nil {
				wins <- v
			}
		}()
	}
	wg.Wait()
	close(wins)
	newCount := 0
	for v := range wins {
		if !v.Replayed {
			newCount++
			actors[1].Character = v.Character
		}
	}
	if newCount != 1 {
		t.Fatalf("concurrent pickup grants=%d", newCount)
	}
	// Expiry metadata survives queue/list paths and excludes later pickup.
	d2 := killForQuestDrop(t, ctx, s, &actors[1], "no-longer-needs")
	if len(d2) != 0 {
		t.Fatal("second actor had no remaining requirement")
	}
}

func TestQuestDropPartyOwnerRuleAndTwoActorFFA(t *testing.T) {
	ctx, s, actors := questDropFixture(t, true)
	created, err := s.MutateParty(ctx, domain.PartyRequest{Session: actors[0], Action: domain.PartyCreate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: actors[0], Action: domain.PartyInvite, TargetID: actors[1].CharacterID, Name: actors[1].Character.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: actors[1], Action: domain.PartyAccept, PartyID: created.After.ID}); err != nil {
		t.Fatal(err)
	}
	d := killForQuestDrop(t, ctx, s, &actors[0], "party", &actors[1])[0]
	if d.QuestDropType != 1 || d.QuestPartyID != created.After.ID {
		t.Fatalf("initial party policy missing: %+v", d)
	}
	pickup, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: d.ObjectID})
	if err != nil {
		t.Fatalf("active same-party pickup rejected: %v", err)
	}
	actors[1].Character = pickup.Character
	// Owner can still produce its own needed item; expire it instead of credit.
	d = killForQuestDrop(t, ctx, s, &actors[0], "expiry", &actors[1])[0]
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_drops SET expires_at=clock_timestamp()-interval '1 second' WHERE object_id=$1`, d.ObjectID); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireItemDrops(ctx, actors[0].Lease, 512)
	if err != nil || len(expired) != 1 || expired[0].QuestID != 1035 {
		t.Fatalf("quest expiry metadata: %+v %v", expired, err)
	}
	if _, err = s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[0], ObjectID: d.ObjectID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("expired quest drop picked up")
	}
}

func TestQuestDropTwoEligibleActorsHaveOneFFAWinner(t *testing.T) {
	ctx, s, actors := questDropFixture(t, true)
	d := killForQuestDrop(t, ctx, s, &actors[0], "two-eligible")[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_item_drops SET quest_owner_until=clock_timestamp()-interval '1 second' WHERE object_id=$1`, d.ObjectID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wins := make(chan int, 2)
	for i := range actors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[i], ObjectID: d.ObjectID}); err == nil {
				wins <- i
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	count := 0
	for range wins {
		count++
	}
	if count != 1 {
		t.Fatalf("two eligible actors acquired one drop %d times", count)
	}
	total := 0
	for _, a := range actors {
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range c.Items {
			if i.TemplateID == 4031802 {
				total += i.Quantity
			}
		}
	}
	if total != 1 {
		t.Fatalf("contended quest item quantity=%d", total)
	}
}
