package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func scrollOutcomePGFixture(t *testing.T) (context.Context, *Store, domain.ScrollRequest) {
	t.Helper()
	usePrivateSchema(t, "scroll_outcome")
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, sessions := scrollFixture(t)
	return ctx, s, domain.ScrollRequest{Session: sessions[0], ClientTick: 917001, SourceSlot: 2, TargetSlot: -11}
}
func TestScrollOutcomePGCommitLostReplyAndImmutableResolve(t *testing.T) {
	ctx, s, q := scrollOutcomePGFixture(t)
	var rolls atomic.Int32
	_, err := s.applyScroll(ctx, q, func() (int, error) { rolls.Add(1); return 17, nil }, func(tx *sql.Tx) error {
		if e := tx.Commit(); e != nil {
			return e
		}
		return errors.New("private seam: committed reply lost")
	})
	if err == nil {
		t.Fatal("lost reply not injected")
	}
	// Receipt resolution is identity-only, independent of a later WZ outage.
	originalWZ := os.Getenv("MAPLEGO_WZ_ROOT")
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	v, found, err := s.ResolveScroll(ctx, q)
	if err != nil || !found || !v.Replayed || rolls.Load() != 1 || v.SuccessRoll != 17 || v.CommittedRevision != q.Session.Character.Revision+1 {
		t.Fatal("same operation resolution", found, err, rolls.Load())
	}
	t.Setenv("MAPLEGO_WZ_ROOT", originalWZ)
	if err = domain.ValidateScrollReceipt(q, v); err != nil {
		t.Fatal(err)
	}
	if potentialItemQuantity(v.Inventory.Character, 2) != 1 || equipmentAt(v.Inventory.Character, -11).WATK != equipmentAt(q.Session.Character, -11).WATK+2 {
		t.Fatal("not exactly one material/stat change")
	}
	again, err := s.applyScroll(ctx, q, func() (int, error) { t.Fatal("replayed RNG"); return 0, nil }, func(*sql.Tx) error { t.Fatal("replayed commit"); return nil })
	if err != nil || !again.Replayed || len(again.Inventory.Changes) != 0 || rolls.Load() != 1 {
		t.Fatal("ordinary replay", err)
	}
	if err = s.SaveCharacter(ctx, q.Session, q.Session.Character); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("old disconnect save not revision fenced", err)
	}
	got, err := s.GetCharacter(ctx, q.Session.CharacterID)
	if err != nil || got.Revision != v.CommittedRevision || potentialItemQuantity(got, 2) != 1 {
		t.Fatal("stale save overwrote commit", err)
	}
	wrong := q
	wrong.SourceSlot = 3
	if _, _, err = s.ResolveScroll(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("changed original hash", err)
	}
	// Legacy result has a real committed aggregate revision, even without the
	// newer marker/plan fields. Resolve retains deltas, normal replay strips.
	legacy := v
	legacy.PlanJSON = nil
	legacy.PlanSHA256 = ""
	legacy.CommittedRevision = 0
	legacy.Replayed = false
	legacy.Inventory.Replayed = false
	raw, _ := json.Marshal(legacy)
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_enhancement_commands SET result=$1 WHERE character_id=$2 AND client_tick=$3`, raw, q.Session.CharacterID, int64(q.ClientTick)); err != nil {
		t.Fatal(err)
	}
	legacyResolved, found, err := s.ResolveScroll(ctx, q)
	if err != nil || !found || legacyResolved.CommittedRevision != v.CommittedRevision || len(legacyResolved.Inventory.Changes) != 3 {
		t.Fatal("legacy committed snapshot", found, err)
	}
	// A later legitimate inventory revision makes the old receipt unsuitable as
	// a fresh live delta. It remains a safe no-delta replay.
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET revision=revision+1 WHERE id=$1`, q.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, found, err = s.ResolveScroll(ctx, q); !found || !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("old delta admitted at later revision", found, err)
	}
	replay, err := s.ApplyScroll(ctx, q)
	if err != nil || len(replay.Inventory.Changes) != 0 || replay.CommittedRevision != v.CommittedRevision || replay.Inventory.Character.Revision != v.CommittedRevision+1 {
		t.Fatal("later revision safe replay", err)
	}
}
func TestScrollOutcomePGRollbackAbsenceAndLockedResolver(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			ctx, s, q := scrollOutcomePGFixture(t)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var draws atomic.Int32
			go func() {
				_, err := s.applyScroll(ctx, q, func() (int, error) { draws.Add(1); close(entered); <-release; return 17, nil }, func(tx *sql.Tx) error {
					if commit {
						if e := tx.Commit(); e != nil {
							return e
						}
					} else {
						if e := tx.Rollback(); e != nil {
							return e
						}
					}
					return errors.New("reply unavailable")
				})
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("Apply never locked")
			}
			lookup, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
			_, _, err := s.ResolveScroll(lookup, q)
			cancel()
			if err == nil {
				t.Fatal("resolver treated in-flight absence as rollback")
			}
			unblock()
			select {
			case err = <-done:
				if err == nil {
					t.Fatal("seam missing error")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("original transaction blocked")
			}
			v, found, err := s.ResolveScroll(ctx, q)
			if err != nil || found != commit || draws.Load() != 1 {
				t.Fatal("post-lock proven outcome", found, err, draws.Load())
			}
			got, err := s.GetCharacter(ctx, q.Session.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				if got.Revision != v.CommittedRevision || potentialItemQuantity(got, 2) != 1 {
					t.Fatal("committed mismatch")
				}
			} else if got.Revision != q.Session.Character.Revision || !reflect.DeepEqual(got.Items, q.Session.Character.Items) || !reflect.DeepEqual(got.Equipment, q.Session.Character.Equipment) {
				t.Fatal("rollback changed assets")
			}
		})
	}
}
func TestScrollOutcomePGCorruptStoredReceiptIsUncertain(t *testing.T) {
	ctx, s, q := scrollOutcomePGFixture(t)
	v, err := s.applyScroll(ctx, q, func() (int, error) { return 17, nil }, (*sql.Tx).Commit)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(v)
	for _, kind := range []string{"quantity", "plan hash", "revision", "JSON type"} {
		t.Run(kind, func(t *testing.T) {
			var bad domain.ScrollResult
			_ = json.Unmarshal(original, &bad)
			switch kind {
			case "quantity":
				bad.Inventory.Changes[0].Quantity = 99
			case "plan hash":
				bad.PlanSHA256 = "bad"
			case "revision":
				bad.CommittedRevision++
			case "JSON type":
			}
			raw, _ := json.Marshal(bad)
			if kind == "JSON type" {
				raw = []byte(`{"Inventory":17}`)
			}
			if _, err = s.db.ExecContext(ctx, `UPDATE asset_enhancement_commands SET result=$1 WHERE character_id=$2 AND client_tick=$3`, raw, q.Session.CharacterID, int64(q.ClientTick)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ApplyScroll(ctx, q); !errors.Is(err, domain.ErrScrollOutcomeUnknown) || errors.Is(err, domain.ErrForbidden) {
				t.Fatal("committed corruption classified as known rollback", err)
			}
			if _, found, err := s.ResolveScroll(ctx, q); !found || !errors.Is(err, domain.ErrScrollOutcomeUnknown) {
				t.Fatal("corrupt receipt resolver", found, err)
			}
			got, e := s.GetCharacter(ctx, q.Session.CharacterID)
			if e != nil || got.Revision != v.CommittedRevision || potentialItemQuantity(got, 2) != 1 {
				t.Fatal("corruption reran mutation", e)
			}
		})
	}
}

func TestScrollOutcomePGExpiryDuringEntropyCannotSpend(t *testing.T) {
	for _, kind := range []string{"lease", "equipment", "material", "white"} {
		t.Run(kind, func(t *testing.T) {
			ctx, s, q := scrollOutcomePGFixture(t)
			var expires int64
			if err := s.db.QueryRowContext(ctx, `SELECT (extract(epoch FROM clock_timestamp())*1000)::bigint+600`).Scan(&expires); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "lease":
				_, err = s.db.ExecContext(ctx, `UPDATE ops_runtime_leases SET expires_at=to_timestamp($1::double precision/1000) WHERE world_id=$2`, expires, q.Session.Lease.WorldID)
			case "equipment", "material", "white":
				inv, slot, id, qty := 1, -11, equipmentAt(q.Session.Character, -11).ItemID, 1
				if kind != "equipment" {
					inv, slot, id, qty = 2, 2, 2043200, 2
				}
				if kind == "white" {
					q.WhiteFlags = 2
					slot, id = 6, 2340000
				}
				// Expiry is a genuine CASH serial overlay in this schema,
				// not a nonexistent expiry column on ordinary asset rows.
				_, err = s.db.ExecContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,'inventory')`, q.Session.AccountID, q.Session.CharacterID, q.Session.Lease.WorldID, id, qty, inv, slot, expires)

			}
			if err != nil {
				t.Fatal(err)
			}
			q.Session.Character, err = s.GetCharacter(ctx, q.Session.CharacterID)
			if err != nil {
				t.Fatal(err)
			}
			before := q.Session.Character
			draws := 0
			_, err = s.applyScroll(ctx, q, func() (int, error) {
				draws++
				time.Sleep(time.Until(time.UnixMilli(expires).Add(30 * time.Millisecond)))
				return 17, nil
			}, func(*sql.Tx) error { t.Fatal("expired operation reached commit"); return nil })
			want := domain.ErrForbidden
			if kind == "lease" {
				want = domain.ErrStaleOwner
			}
			if !errors.Is(err, want) || draws != 1 {
				t.Fatal("expiry did not reject after one draw", kind, err, draws)
			}
			got, err := s.GetCharacter(ctx, q.Session.CharacterID)
			if err != nil || got.Revision != before.Revision || !reflect.DeepEqual(got.Items, before.Items) || !reflect.DeepEqual(got.Equipment, before.Equipment) {
				t.Fatal("expiry spent assets", kind, err)
			}
			var receipts int
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_enhancement_commands WHERE character_id=$1`, q.Session.CharacterID).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatal("expiry stored receipt", err, receipts)
			}
		})
	}
}

func TestScrollOutcomePGWhiteSelectionAndLegacyTerminalReplay(t *testing.T) {
	ctx, s, q := scrollOutcomePGFixture(t)
	// Use the real inventory command: moving slot6 to10 preserves the live
	// slice order. A subsequent granted/received white at8 is appended there,
	// while ApplyScroll reloads the SQL rows ORDER BY inv_type,slot.
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: q.Session, ClientTick: 916999, InvType: 2, Source: 6, Destination: 10, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	q.Session.Character = moved.Character
	var lowID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,8,2340000,1) RETURNING id`, q.Session.CharacterID).Scan(&lowID); err != nil {
		t.Fatal(err)
	}
	sorted, err := s.GetCharacter(ctx, q.Session.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	var low domain.InventoryItem
	for _, i := range sorted.Items {
		if i.ID == lowID {
			low = i
		}
	}
	q.Session.Character.Items = append(q.Session.Character.Items, low)
	q.WhiteFlags = 2
	firstWhiteSlot := 0
	for _, i := range q.Session.Character.Items {
		if i.TemplateID == 2340000 {
			firstWhiteSlot = i.Slot
			break
		}
	}
	if firstWhiteSlot != 10 {
		t.Fatal("fixture did not preserve real unsorted live order")
	}
	draws := 0
	v, err := s.applyScroll(ctx, q, func() (int, error) { draws++; return 17, nil }, (*sql.Tx).Commit)
	if err != nil {
		t.Fatal(err)
	}
	var p domain.ScrollOutcomePlan
	if err = json.Unmarshal(v.PlanJSON, &p); err != nil {
		t.Fatal(err)
	}
	if draws != 1 || p.WhiteBefore == nil || p.WhiteBefore.ID != lowID || p.WhiteBefore.Slot != 8 || v.Inventory.Changes[1].Mode != 3 || v.Inventory.Changes[1].Slot != 8 || potentialItemQuantity(v.Inventory.Character, 10) != 2 || potentialItemQuantity(v.Inventory.Character, 8) != 0 {
		t.Fatal("SQL did not select min-slot actual white", p.WhiteBefore, draws)
	}
	// Turn only this private receipt into its historical representation.
	// Post-commit callers have no slot8; surviving slot10 must not be consumed
	// or mistaken for the historical white before-image.
	legacy := v
	legacy.PlanJSON = nil
	legacy.PlanSHA256 = ""
	legacy.CommittedRevision = 0
	raw, _ := json.Marshal(legacy)
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_enhancement_commands SET result=$1 WHERE character_id=$2 AND client_tick=$3`, raw, q.Session.CharacterID, int64(q.ClientTick)); err != nil {
		t.Fatal(err)
	}
	post := q
	post.Session.Character = v.Inventory.Character
	replay, err := s.applyScroll(ctx, post, func() (int, error) { t.Fatal("legacy replay redrew"); return 0, nil }, func(*sql.Tx) error { t.Fatal("legacy replay committed again"); return nil })
	if err != nil || !replay.Replayed || len(replay.Inventory.Changes) != 0 || potentialItemQuantity(replay.Inventory.Character, 10) != 2 || replay.CommittedRevision != v.CommittedRevision {
		t.Fatal("legacy terminal-white replay rejected or spent another white", err)
	}
	resolved, found, err := s.ResolveScroll(ctx, post)
	if err != nil || !found || len(resolved.Inventory.Changes) != 4 || resolved.Inventory.Changes[1].Slot != 8 || potentialItemQuantity(resolved.Inventory.Character, 10) != 2 {
		t.Fatal("legacy original delta lost terminal slot", found, err)
	}
}
