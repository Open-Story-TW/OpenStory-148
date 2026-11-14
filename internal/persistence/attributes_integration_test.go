package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestAttributeAllocationIsAtomicAndBoundToNativeTick(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET ap=5 WHERE id=$1`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	req := domain.AttributeRequest{Session: actor, ClientTick: 1357, Stat: 0x40}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := store.AllocateAttribute(ctx, req); e != nil {
				t.Errorf("AP allocation/replay: %v", e)
			}
		}()
	}
	wg.Wait()
	loaded, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || loaded.STR != actor.Character.STR+1 || loaded.AP != 4 || loaded.Revision != actor.Character.Revision+1 {
		t.Fatalf("AP allocated more than once: %+v %v", loaded, err)
	}
	req.Stat = 0x80
	if _, err = store.AllocateAttribute(ctx, req); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reused tick changed target: %v", err)
	}
	actor.Character = loaded
	for i, stat := range []uint64{0x80, 0x100, 0x200} {
		result, err := store.AllocateAttribute(ctx, domain.AttributeRequest{Session: actor, ClientTick: uint32(1358 + i), Stat: stat})
		if err != nil {
			t.Fatal(err)
		}
		actor.Character = result.Character
	}
	if actor.Character.AP != 1 || actor.Character.DEX != 6 || actor.Character.INT != 5 || actor.Character.LUK != 5 {
		t.Fatalf("other AP targets: %+v", actor.Character)
	}
	bad := domain.AttributeRequest{Session: actor, ClientTick: 2000, Stat: 0xc0}
	if _, err = store.AllocateAttribute(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("combined stat mask accepted: %v", err)
	}
	bad.Stat, bad.Session.Generation = 0x40, bad.Session.Generation+1
	if _, err = store.AllocateAttribute(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale AP owner accepted: %v", err)
	}
}

func TestAttributeReceiptFailureAndCapDoNotSpendPoints(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET ap=2 WHERE id=$1`, actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("attribute_fail_%d", actor.CharacterID)
	_, err = store.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test AP receipt failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_attribute_commands FOR EACH ROW WHEN (NEW.character_id=%d) EXECUTE FUNCTION %s()`, name, name, actor.CharacterID, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_attribute_commands; DROP FUNCTION IF EXISTS %s()", name, name))
	})
	if _, err = store.AllocateAttribute(ctx, domain.AttributeRequest{Session: actor, ClientTick: 42, Stat: 0x40}); err == nil {
		t.Fatal("injected receipt failure accepted")
	}
	loaded, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || loaded.STR != actor.Character.STR || loaded.AP != 2 || loaded.Revision != actor.Character.Revision {
		t.Fatalf("partial AP commit: %+v %v", loaded, err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE game_characters SET str=$1 WHERE id=$2`, baseAttributeLimit(), actor.CharacterID); err != nil {
		t.Fatal(err)
	}
	actor.Character, err = store.GetCharacter(ctx, actor.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AllocateAttribute(ctx, domain.AttributeRequest{Session: actor, ClientTick: 43, Stat: 0x40}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("attribute cap accepted: %v", err)
	}
}

func TestAutoAttributeAllocationCommitsWholeBatchOnce(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	a := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET ap=10 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	q := domain.AttributeRequest{Session: a, ClientTick: 3001, Allocations: []domain.AttributeAllocation{{Stat: 0x40, Amount: 3}, {Stat: 0x80, Amount: 2}}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.AllocateAttribute(ctx, q); err != nil {
				t.Errorf("batch/replay: %v", err)
			}
		}()
	}
	wg.Wait()
	c, err := store.GetCharacter(ctx, a.CharacterID)
	if err != nil || c.STR != a.Character.STR+3 || c.DEX != a.Character.DEX+2 || c.AP != 5 || c.Revision != a.Character.Revision+1 {
		t.Fatalf("non-atomic/repeated batch: %+v %v", c, err)
	}
	q.Allocations[1].Amount++
	if _, err = store.AllocateAttribute(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("altered batch replay: %v", err)
	}
	if _, err = store.AllocateAttribute(ctx, domain.AttributeRequest{Session: a, ClientTick: q.ClientTick, Stat: 0x40}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("single/batch tick collision: %v", err)
	}
	q.ClientTick++
	q.Session.Generation++
	if _, err = store.AllocateAttribute(ctx, q); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale batch owner: %v", err)
	}
}

func TestAutoAttributeValidationRejectsWholeBatch(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	a := actors[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE game_characters SET ap=5,dex=$2 WHERE id=$1`, a.CharacterID, baseAttributeLimit()-1); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = store.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		batch []domain.AttributeAllocation
		want  error
	}{
		{"cap on second stat", []domain.AttributeAllocation{{Stat: 0x40, Amount: 1}, {Stat: 0x80, Amount: 2}}, domain.ErrForbidden},
		{"duplicate target cannot bypass cap", []domain.AttributeAllocation{{Stat: 0x80, Amount: 1}, {Stat: 0x80, Amount: 1}}, domain.ErrForbidden},
		{"insufficient total AP", []domain.AttributeAllocation{{Stat: 0x40, Amount: 3}, {Stat: 0x100, Amount: 3}}, domain.ErrInsufficient},
		{"negative amount", []domain.AttributeAllocation{{Stat: 0x40, Amount: 2}, {Stat: 0x80, Amount: -1}}, domain.ErrInvalidInput},
		{"full-width invalid mask", []domain.AttributeAllocation{{Stat: 0x100000040, Amount: 1}}, domain.ErrInvalidInput},
		{"unsupported HP allocation", []domain.AttributeAllocation{{Stat: 0x800, Amount: 1}}, domain.ErrInvalidInput},
		{"zero total", []domain.AttributeAllocation{{Stat: 0x40, Amount: 0}}, domain.ErrInvalidInput},
		{"oversized amount", []domain.AttributeAllocation{{Stat: 0x40, Amount: 2147483647}}, domain.ErrInvalidInput},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.AllocateAttribute(ctx, domain.AttributeRequest{Session: a, ClientTick: uint32(4000 + i), Allocations: tc.batch})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			c, err := store.GetCharacter(ctx, a.CharacterID)
			if err != nil || c.STR != a.Character.STR || c.DEX != a.Character.DEX || c.AP != a.Character.AP || c.Revision != a.Character.Revision {
				t.Fatalf("partial allocation: %+v %v", c, err)
			}
		})
	}
}
