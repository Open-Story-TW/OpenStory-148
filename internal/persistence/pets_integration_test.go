package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"maple-go-server/ent/migrate"
	"maple-go-server/internal/domain"
)

// usePrivateSchema installs every migration in a generated schema of the
// isolated Go database and points the store at it for this test only.
func usePrivateSchema(t *testing.T, prefix string) {
	t.Helper()
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("isolated PostgreSQL required")
	}
	pattern := regexp.MustCompile(`^test_` + prefix + `_[0-9a-f]{12}$`)
	original := os.Getenv("MAPLEGO_DATABASE_URL")
	admin, err := sql.Open("pgx", original)
	if err != nil {
		t.Fatal("open test database")
	}
	suffix, err := randomToken(6)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + prefix + "_" + suffix
	if !pattern.MatchString(schema) {
		t.Fatal("invalid generated test schema")
	}
	if _, err = admin.ExecContext(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if !pattern.MatchString(schema) {
			t.Error("refusing unexpected schema cleanup")
			return
		}
		if _, e := admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); e != nil {
			t.Error("private schema cleanup", e)
		}
	})
	uri, err := url.Parse(original)
	if err != nil || uri.Scheme != "postgres" && uri.Scheme != "postgresql" {
		t.Fatal("private schema requires a PostgreSQL URI")
	}
	params := uri.Query()
	params.Set("search_path", schema)
	uri.RawQuery = params.Encode()
	probe, err := sql.Open("pgx", uri.String())
	if err != nil {
		t.Fatal("open private schema connection")
	}
	defer probe.Close()
	var current string
	if err = probe.QueryRow(`SELECT current_schema()`).Scan(&current); err != nil || current != schema {
		t.Fatal("search_path did not select the generated test schema")
	}
	if err = migrate.Apply(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_DATABASE_URL", uri.String())
	t.Setenv("MAPLEGO_WZ_ROOT", "")
}

func insertTestPet(t *testing.T, ctx context.Context, s *Store, c domain.CharacterSession, slot, template int, name string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,state) VALUES($1,$2,$3,0,$4,1,5,$5,'inventory') RETURNING id`, c.AccountID, c.CharacterID, c.Lease.WorldID, template, slot).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,5,$2,$3,1)`, c.CharacterID, slot, template); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_pets(cash_id,template_id,name) VALUES($1,$2,$3)`, id, template, name); err != nil {
		t.Fatal(err)
	}
	return id
}

func petFixture(t *testing.T) (context.Context, *Store, domain.CharacterSession, int64, int64) {
	t.Helper()
	usePrivateSchema(t, "pets")
	ctx, s, actors := inventoryFixture(t)
	owner := actors[0]
	a := insertTestPet(t, ctx, s, owner, 1, 5000000, "Kitty")
	b := insertTestPet(t, ctx, s, owner, 2, 5000001, "Puppy")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,9,2120000,3)`, owner.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if owner.Character, err = s.GetCharacter(ctx, owner.CharacterID); err != nil {
		t.Fatal(err)
	}
	if len(owner.Character.Pets) != 2 {
		t.Fatalf("character load pets: %+v", owner.Character.Pets)
	}
	return ctx, s, owner, a, b
}

func petOp(name string) string { return "pet-test-" + name + "-0123456789" }

func petSummon(p []domain.Pet, id int64) int {
	for _, v := range p {
		if v.CashID == id {
			return v.Summon
		}
	}
	return -1
}

func TestPetSummonLeadRecallAndReplay(t *testing.T) {
	ctx, s, owner, a, b := petFixture(t)
	r, err := s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("a1"), Slot: 1})
	if err != nil || petSummon(r.Pets, a) != 1 || len(r.Changes) != 1 {
		t.Fatalf("summon a: %+v %v", r, err)
	}
	if _, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("a1"), Slot: 1}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("replayed summon re-applied: %v", err)
	}
	r, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("b1"), Slot: 2, Lead: true})
	if err != nil || petSummon(r.Pets, b) != 1 || petSummon(r.Pets, a) != 2 {
		t.Fatalf("lead summon: %+v %v", r.Pets, err)
	}
	r, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("b2"), Slot: 2})
	if err != nil || petSummon(r.Pets, b) != 0 || !r.Changes[0].Recalled {
		t.Fatalf("recall: %+v %v", r, err)
	}
	if _, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("x1"), Slot: 7}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty slot: %v", err)
	}
	fm := owner
	fm.Character.MapID = domain.FreeMarketMapID
	if _, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: fm, OperationID: petOp("fm"), Slot: 2}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("free market summon: %v", err)
	}
	reloaded, err := s.GetCharacter(ctx, owner.CharacterID)
	if err != nil || petSummon(reloaded.Pets, a) != 2 || petSummon(reloaded.Pets, b) != 0 {
		t.Fatalf("summon state not durable: %+v %v", reloaded.Pets, err)
	}
	stale := owner
	stale.Generation++
	if _, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: stale, OperationID: petOp("st"), Slot: 2}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale session: %v", err)
	}
}

func TestPetFeedConsumesOnceAndLevels(t *testing.T) {
	ctx, s, owner, a, _ := petFixture(t)
	if _, err := s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("sum"), Slot: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_pets SET fullness=50 WHERE cash_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins []domain.PetResult
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.FeedPet(ctx, domain.PetFeedRequest{Session: owner, OperationID: petOp("feed" + string(rune('a'+i))), ClientTick: uint32(i), Slot: 9, TemplateID: 2120000, GainCloseness: true})
			if err == nil {
				mu.Lock()
				wins = append(wins, r)
				mu.Unlock()
			} else if !errors.Is(err, domain.ErrStaleOwner) {
				t.Errorf("feed %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatalf("one revision allowed %d feeds", len(wins))
	}
	r := wins[0]
	after := r.Changes[0].After
	if !r.Success || after.Fullness != 80 || after.Closeness != 1 || after.Level != 2 || !r.Changes[0].LevelUp || r.Character == nil {
		t.Fatalf("feed result: %+v", r)
	}
	var quantity int
	if err := s.db.QueryRowContext(ctx, `SELECT quantity FROM asset_items WHERE character_id=$1 AND inv_type=2 AND slot=9`, owner.CharacterID).Scan(&quantity); err != nil || quantity != 2 {
		t.Fatalf("food consumed %d err=%v", quantity, err)
	}
	next := owner
	next.Character = *r.Character
	if _, err := s.FeedPet(ctx, domain.PetFeedRequest{Session: next, OperationID: petOp("feed-second"), Slot: 9, TemplateID: 2120000}); err != nil {
		t.Fatalf("second feed with current revision: %v", err)
	}
	if _, err := s.FeedPet(ctx, domain.PetFeedRequest{Session: next, OperationID: petOp("wrongitem"), Slot: 9, TemplateID: 2120001}); !errors.Is(err, domain.ErrStaleOwner) && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wrong template: %v", err)
	}
}

func TestPetCommandAndDecay(t *testing.T) {
	ctx, s, owner, a, b := petFixture(t)
	for i, slot := range []int{1, 2} {
		if _, err := s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("sum" + string(rune('0'+i))), Slot: slot}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.CommandPet(ctx, domain.PetCommandRequest{Session: owner, OperationID: petOp("cmd"), Index: 0, Increase: 3})
	if err != nil || r.Changes[0].After.Closeness != 3 || r.Changes[0].After.Level != 3 {
		t.Fatalf("command: %+v %v", r, err)
	}
	if _, err = s.CommandPet(ctx, domain.PetCommandRequest{Session: owner, OperationID: petOp("cmd"), Index: 0, Increase: 3}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("command replay: %v", err)
	}
	now := time.Now()
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_items SET expires_at=$2 WHERE id=$1`, b, now.Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	r, err = s.DecayPets(ctx, domain.PetDecayRequest{Session: owner, OperationID: petOp("decay1"), Hunger: map[int64]int{a: 10, b: 10}, Now: now})
	if err != nil || len(r.Changes) != 2 {
		t.Fatalf("decay: %+v %v", r, err)
	}
	for _, ch := range r.Changes {
		switch ch.After.CashID {
		case a:
			if ch.After.Fullness != 90 || !ch.After.Summoned() {
				t.Fatalf("hunger: %+v", ch)
			}
		case b:
			if ch.Reason != 2 || ch.After.Summoned() || ch.After.Fullness != 100 {
				t.Fatalf("expiry: %+v", ch)
			}
		}
	}
	r, err = s.DecayPets(ctx, domain.PetDecayRequest{Session: owner, OperationID: petOp("decay2"), Hunger: map[int64]int{a: 85}, Now: now})
	if err != nil || len(r.Changes) != 1 || r.Changes[0].Reason != 1 || r.Changes[0].After.Fullness != 15 || r.Changes[0].After.Summoned() {
		t.Fatalf("starved pet: %+v %v", r, err)
	}
	if _, err = s.SummonPet(ctx, domain.PetSummonRequest{Session: owner, OperationID: petOp("dead"), Slot: 2}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expired pet summoned: %v", err)
	}
}

func TestPetGrantRequiresOfflineAndIsIdempotent(t *testing.T) {
	ctx, s, owner, _, _ := petFixture(t)
	req := domain.PetGrantRequest{OperationID: petOp("grant-online"), AccountID: owner.AccountID, CharacterID: owner.CharacterID, TemplateID: 5000002, Name: "Bunny"}
	if _, err := s.GrantPet(ctx, req); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("online grant: %v", err)
	}
	extra := partyExtraActor(t, ctx, s, owner.Lease, 9)
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM asset_cash_items WHERE account_id=$1`, extra.AccountID)
	})
	if err := s.ReleaseCharacter(ctx, extra); err != nil {
		t.Fatal(err)
	}
	req = domain.PetGrantRequest{OperationID: petOp("grant-off"), AccountID: extra.AccountID, CharacterID: extra.CharacterID, TemplateID: 5000002, Name: "小兔", PeriodDays: 90}
	pet, err := s.GrantPet(ctx, req)
	if err != nil || pet.CashID == 0 || pet.Slot != 1 || pet.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("grant: %+v %v", pet, err)
	}
	again, err := s.GrantPet(ctx, req)
	if err != nil || again.CashID != pet.CashID {
		t.Fatalf("grant replay: %+v %v", again, err)
	}
	list, err := s.ListPets(ctx, extra.AccountID, extra.CharacterID)
	if err != nil || len(list) != 1 || list[0].Name != "小兔" {
		t.Fatalf("list: %+v %v", list, err)
	}
	req.OperationID, req.Name = petOp("grant-bad"), "ThisNameIsTooLong"
	if _, err = s.GrantPet(ctx, req); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("long name: %v", err)
	}
}

func TestCashShopPetPurchaseCreatesSummonablePet(t *testing.T) {
	usePrivateSchema(t, "petcash")
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	if _, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadCashWallet(ctx, cs); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	bought, err := s.PurchaseCash(ctx, domain.CashPurchaseRequest{Session: cs, OperationID: petOp("buy"), SN: 10003328, Currency: 1})
	if err != nil || bought.Item.TemplateID != 5000090 || bought.Item.InvType != 5 {
		t.Fatalf("pet purchase %+v %v", bought, err)
	}
	var name string
	if err = s.db.QueryRowContext(ctx, `SELECT name FROM asset_pets WHERE cash_id=$1`, bought.Item.ID).Scan(&name); err != nil || name == "" {
		t.Fatalf("pet row %q %v", name, err)
	}
	moved, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: petOp("withdraw"), CashID: bought.Item.ID})
	if err != nil || len(moved.Character.Pets) != 1 || moved.Character.Pets[0].CashID != bought.Item.ID || moved.Character.Pets[0].Slot < 1 {
		t.Fatalf("withdraw pet %+v %v", moved.Character.Pets, err)
	}
}
