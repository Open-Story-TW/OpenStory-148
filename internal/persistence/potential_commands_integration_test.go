package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// These are real PostgreSQL transaction tests, deliberately skipped while the
// existing configured test PG is unavailable. They never start a service or
// provision another runtime; usePrivateSchema is the existing cleanup helper.
func potentialCommandFixture(t *testing.T) (context.Context, *Store, domain.CharacterSession) {
	t.Helper()
	usePrivateSchema(t, "potential")
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	t.Setenv("MAPLEGO_WZ_ROOT", writePotentialCommandTestWZ(t))
	for slot, id := range map[int]int{2: 2049400, 3: 2049701, 4: 2460000} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,$3,2)`, a.CharacterID, slot, id); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit test setup funding, never a launcher purchase/production grant.
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=100000 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, a
}

func potentialItemQuantity(c domain.Character, slot int) int {
	for _, item := range c.Items {
		if item.InvType == 2 && item.Slot == slot {
			return item.Quantity
		}
	}
	return 0
}

func potentialReload(t *testing.T, ctx context.Context, s *Store, a domain.CharacterSession) domain.CharacterSession {
	t.Helper()
	c, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = c
	return a
}

func potentialCount(t *testing.T, ctx context.Context, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPotentialPGAtomicSealMagnifyReplayAndOrdinaryIsolation(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	before := equipmentAt(a.Character, -11)
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 91001, SourceSlot: 2, TargetSlot: -11}
	var rngCalls atomic.Int64
	randN := func(bound int) (int, error) { rngCalls.Add(1); return bound - 1, nil }
	var results [2]domain.PotentialCommandResult
	var errs [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.applyPotential(ctx, r, randN, (*sql.Tx).Commit)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Replayed == results[1].Replayed || rngCalls.Load() != 2 {
		t.Fatal("same operation rerolled or failed", errs, rngCalls.Load())
	}
	a = potentialReload(t, ctx, s, a)
	e := equipmentAt(a.Character, -11)
	if e.ID != before.ID || e.ItemID != before.ItemID || e.UpgradeSlots != before.UpgradeSlots || e.ScrollLevel != before.ScrollLevel ||
		!e.Potential.Present || e.Potential.RawState != 1 || e.Potential.Options[0] != 65519 || e.Potential.Flags != 65535 || potentialItemQuantity(a.Character, 2) != 1 || a.Character.Mesos != 100000 {
		t.Fatal("seal did not conserve equipment/material/mesos", before, e, a.Character.Mesos)
	}
	if _, err := s.ApplyScroll(ctx, domain.ScrollRequest{Session: a, ClientTick: r.ClientTick, SourceSlot: r.SourceSlot, TargetSlot: r.TargetSlot}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("ordinary scroll consumed a potential receipt", err)
	}
	changed := r
	changed.Action = domain.PotentialMagnify
	if _, err := s.ApplyPotential(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("same tick changed family/action", err)
	}
	m := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialMagnify, ClientTick: 91002, SourceSlot: 4, TargetSlot: -11}
	mag, err := s.applyPotential(ctx, m, func(int) (int, error) { return 0, nil }, (*sql.Tx).Commit)
	if err != nil {
		t.Fatal(err)
	}
	if mag.Status != 1 || mag.MesoFee != 1000 || mag.Inventory.Character.Mesos != 99000 || potentialItemQuantity(mag.Inventory.Character, 4) != 1 ||
		equipmentAt(mag.Inventory.Character, -11).Potential.Options != [5]uint16{10001, 10001, 0, 0, 0} {
		t.Fatal("magnify debit/grant mismatch", mag)
	}
	if len(mag.Inventory.Changes) != 3 || mag.Inventory.Changes[0].InvType != 2 || mag.Inventory.Changes[0].Mode != 1 ||
		mag.Inventory.Changes[1].Mode != 3 || mag.Inventory.Changes[2].Mode != 0 || mag.Inventory.Changes[2].Equipment.ID != before.ID {
		t.Fatal("publication batch is out of order", mag.Inventory.Changes)
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_meso_ledger WHERE operation_id=$1 AND delta=-1000 AND balance_after=99000`, mag.OperationID); n != 1 {
		t.Fatal("fee ledger missing or duplicated", n)
	}
	resolved, found, err := s.ResolvePotential(ctx, m)
	if err != nil || !found || !resolved.Replayed || len(resolved.Inventory.Changes) != 3 || !bytes.Equal(resolved.PlanJSON, mag.PlanJSON) {
		t.Fatal("read-only resolution lost the committed delta", found, resolved, err)
	}
	replay, err := s.applyPotential(ctx, m, func(int) (int, error) { t.Fatal("replay called RNG"); return 0, nil }, (*sql.Tx).Commit)
	if err != nil || !replay.Replayed || len(replay.Inventory.Changes) != 0 || replay.Inventory.Character.Mesos != 99000 || !bytes.Equal(replay.PlanJSON, mag.PlanJSON) {
		t.Fatal("normal replay republished or charged", replay, err)
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_enhancement_commands WHERE character_id=$1`, a.CharacterID); n != 2 {
		t.Fatal("duplicate or unrecorded transaction", n)
	}
}

func TestPotentialPGLostCommitReplyResolvesWithoutWZAndLaterRevisionCannotPublish(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	sealed, err := s.applyPotential(ctx, domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 92000, SourceSlot: 2, TargetSlot: -11},
		func(n int) (int, error) { return n - 1, nil }, (*sql.Tx).Commit)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = sealed.Inventory.Character
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialMagnify, ClientTick: 92001, SourceSlot: 4, TargetSlot: -11}
	result, err := s.applyPotential(ctx, r, func(int) (int, error) { return 0, nil }, func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF // actual COMMIT succeeded; only response lost.
	})
	if !errors.Is(err, domain.ErrPotentialOutcomeUnknown) || !reflect.DeepEqual(result, domain.PotentialCommandResult{}) {
		t.Fatal("ambiguous commit was called rollback/success", result, err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir()) // Resolve must not reload any WZ.
	resolved, found, err := s.ResolvePotential(ctx, r)
	if err != nil || !found || resolved.Status != 1 || len(resolved.Inventory.Changes) != 3 || potentialItemQuantity(resolved.Inventory.Character, 4) != 1 ||
		equipmentAt(resolved.Inventory.Character, -11).Potential.RawState != 17 || resolved.MesoFee != 1000 {
		t.Fatal("lost reply could not be resolved read-only", resolved, found, err)
	}
	var receipt domain.PotentialCommandResult
	var raw []byte
	if err = s.db.QueryRowContext(ctx, `SELECT result FROM asset_enhancement_commands WHERE character_id=$1 AND client_tick=$2`, a.CharacterID, int64(r.ClientTick)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &receipt); err != nil || !bytes.Equal(receipt.PlanJSON, resolved.PlanJSON) || receipt.PlanSHA256 != resolved.PlanSHA256 {
		t.Fatal("JSONB changed the frozen plan", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET revision=revision+1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, found, err = s.ResolvePotential(ctx, r); !found || !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("obsolete delta was eligible for publication", found, err)
	}
}

func TestPotentialPGResolverWaitsForOriginalCommitOrRollback(t *testing.T) {
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("existing configured PostgreSQL required; both concurrency branches are unexecuted")
	}
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			ctx, s, a := potentialCommandFixture(t)
			r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 93001, SourceSlot: 2, TargetSlot: -11}
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var once sync.Once
			done := make(chan error, 1)
			go func() {
				_, err := s.applyPotential(ctx, r, func(n int) (int, error) {
					once.Do(func() { close(entered); <-release })
					if rollback {
						return 0, io.ErrUnexpectedEOF
					}
					return n - 1, nil
				}, (*sql.Tx).Commit)
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			resolved := make(chan struct {
				found bool
				err   error
			}, 1)
			go func() {
				_, found, err := s.ResolvePotential(ctx, r)
				resolved <- struct {
					found bool
					err   error
				}{found, err}
			}()
			// Observe the actual blocked FOR UPDATE, not just a goroutine that
			// may not have been scheduled yet. No unrelated test uses this exact
			// identity-only query, and these generated-schema tests are serial.
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waitLock:
			for {
				var blocked int
				if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query=$1`,
					`SELECT id,account_id,world_id,revision FROM game_characters WHERE id=$1 FOR UPDATE`).Scan(&blocked); err != nil {
					unblock()
					<-done
					t.Fatal(err)
				}
				if blocked > 0 {
					break waitLock
				}
				select {
				case result := <-resolved:
					unblock()
					<-done
					t.Fatal("resolver reported absence before the original ended", result)
				case <-deadline.C:
					unblock()
					<-done
					t.Fatal("resolver never reached the original transaction's SQL lock")
				case <-poll.C:
				}
			}
			unblock()
			if err := <-done; (err != nil) != rollback {
				t.Fatal("unexpected original outcome", err)
			}
			answer := <-resolved
			if answer.err != nil || answer.found == rollback {
				t.Fatal("resolver did not observe commit/rollback", answer)
			}
			loaded := potentialReload(t, ctx, s, a)
			want := 1
			if rollback {
				want = 2
			}
			if potentialItemQuantity(loaded.Character, 2) != want {
				t.Fatal("material changed outside committed transaction")
			}
		})
	}
}

func TestPotentialPGFeeMetadataAndMaterialRollbackAfterSpend(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	for i := range a.Character.Equipment {
		if a.Character.Equipment[i].Slot == -11 {
			a.Character.Equipment[i].Potential = domain.EquipmentPotential{Present: true, RawState: 1, Options: [5]uint16{65519}, Flags: 65535}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistEquipmentMetadata(ctx, tx, a.Character); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a = potentialReload(t, ctx, s, a)
	before := a.Character
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialMagnify, ClientTick: 95001, SourceSlot: 4, TargetSlot: -11}
	result, err := s.applyPotential(ctx, r, func(int) (int, error) { return 0, nil }, func(*sql.Tx) error { return io.ErrUnexpectedEOF })
	if !errors.Is(err, domain.ErrPotentialOutcomeUnknown) || !reflect.DeepEqual(result, domain.PotentialCommandResult{}) {
		t.Fatal("unproven failure claimed rollback or success", result, err)
	}
	if _, found, err := s.ResolvePotential(ctx, r); err != nil || found {
		t.Fatal("rolled back transaction left receipt", found, err)
	}
	a = potentialReload(t, ctx, s, a)
	if a.Character.Revision != before.Revision || a.Character.Mesos != before.Mesos || potentialItemQuantity(a.Character, 4) != 2 || equipmentAt(a.Character, -11) != equipmentAt(before, -11) {
		t.Fatal("material/fee/equipment escaped transaction rollback")
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_meso_ledger WHERE operation_id=$1`, potentialOperationID(r)); n != 0 {
		t.Fatal("rollback left fee ledger", n)
	}
}

func TestPotentialPGLeaseExpiryDuringPlanningRollsBackBeforeSpend(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	before := a.Character
	if _, err := s.db.ExecContext(ctx, `UPDATE ops_runtime_leases SET expires_at=clock_timestamp()+interval '150 milliseconds' WHERE world_id=$1`, a.Lease.WorldID); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 96001, SourceSlot: 2, TargetSlot: -11}
	_, err := s.applyPotential(ctx, r, func(n int) (int, error) { once.Do(func() { time.Sleep(200 * time.Millisecond) }); return n - 1, nil }, (*sql.Tx).Commit)
	if !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("expired world lease spent material", err)
	}
	a = potentialReload(t, ctx, s, a)
	if a.Character.Revision != before.Revision || potentialItemQuantity(a.Character, 2) != 2 || equipmentAt(a.Character, -11) != equipmentAt(before, -11) {
		t.Fatal("expiry denial changed metadata/material")
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_enhancement_commands WHERE character_id=$1`, a.CharacterID); n != 0 {
		t.Fatal("expiry denial left receipt", n)
	}
}

func TestPotentialPGBagSealRequiresFreshSQLLegendarySpirit(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	before := equipmentAt(a.Character, -11)
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_equipment SET slot=1 WHERE id=$1`, before.ID); err != nil {
		t.Fatal(err)
	}
	a = potentialReload(t, ctx, s, a)
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 97001, SourceSlot: 2, TargetSlot: 1, LegendarySpirit: true}
	// A stale or fabricated world-side skill list must not authorize spending.
	r.Session.Character.Skills = []domain.SkillState{{ID: 1003, Level: 1}}
	if _, err := s.applyPotential(ctx, r, func(int) (int, error) { t.Fatal("snapshot-only spirit called RNG"); return 0, nil }, (*sql.Tx).Commit); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("SQL skill authority bypassed", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level,expires_at) VALUES($1,1003,1,0,clock_timestamp()-interval '1 second')`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a = potentialReload(t, ctx, s, a)
	r.Session = a
	if _, err := s.applyPotential(ctx, r, func(int) (int, error) { t.Fatal("expired SQL spirit called RNG"); return 0, nil }, (*sql.Tx).Commit); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("expired learned skill authorized material spending", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE game_skills SET expires_at=NULL WHERE character_id=$1 AND skill_id=1003`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a = potentialReload(t, ctx, s, a)
	r.Session = a
	result, err := s.applyPotential(ctx, r, func(n int) (int, error) { return n - 1, nil }, (*sql.Tx).Commit)
	if err != nil || result.Status != 1 || potentialItemQuantity(result.Inventory.Character, 2) != 1 || equipmentAt(result.Inventory.Character, 1).ID != before.ID || equipmentAt(result.Inventory.Character, 1).Potential.RawState != 1 {
		t.Fatal("fresh SQL spirit could not seal in bag", result, err)
	}
}

func TestPotentialPGHyperBodyExpiresDuringTransactionClampsWithoutHealingOrChangingBase(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET base_max_hp=100,base_max_mp=100,vitals_model=1,max_hp=160,max_mp=160,hp=150,mp=150 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(2 * time.Second)
	for _, stat := range []string{"MAXHP", "MAXMP"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,1301007,20,$2,60,$3)`, a.CharacterID, stat, expiry); err != nil {
			t.Fatal(err)
		}
	}
	a = potentialReload(t, ctx, s, a)
	if a.Character.MaxHP != 160 || a.Character.MaxMP != 160 || a.Character.HP != 150 || a.Character.MP != 150 {
		t.Fatal("test's real persisted Hyper Body was not active", a.Character)
	}
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 98001, SourceSlot: 2, TargetSlot: -11}
	var once sync.Once
	result, err := s.applyPotential(ctx, r, func(n int) (int, error) {
		once.Do(func() {
			if delay := time.Until(expiry) + 10*time.Millisecond; delay > 0 {
				time.Sleep(delay)
			}
		})
		return n - 1, nil
	}, (*sql.Tx).Commit)
	if err != nil {
		t.Fatal(err)
	}
	c := result.Inventory.Character
	if c.MaxHP != 100 || c.MaxMP != 100 || c.HP != 100 || c.MP != 100 || c.BaseMaxHP != 100 || c.BaseMaxMP != 100 {
		t.Fatal("expiry inside transaction escaped final clamp or changed base", c)
	}
	var hp, mp, maxHP, maxMP, baseHP, baseMP int
	if err := s.db.QueryRowContext(ctx, `SELECT hp,mp,max_hp,max_mp,base_max_hp,base_max_mp FROM game_characters WHERE id=$1`, a.CharacterID).
		Scan(&hp, &mp, &maxHP, &maxMP, &baseHP, &baseMP); err != nil {
		t.Fatal(err)
	}
	if hp > maxHP || mp > maxMP || hp != 100 || mp != 100 || baseHP != 100 || baseMP != 100 {
		t.Fatal("SQL persisted impossible post-expiry vitals", hp, mp, maxHP, maxMP, baseHP, baseMP)
	}
	// A later legitimate runtime damage snapshot is retained, never filled by
	// another metadata command after the timed buff has already expired.
	a.Character = c
	a.Character.HP, a.Character.MP = 60, 40
	r.Session, r.SourceSlot, r.ClientTick = a, 3, 98002
	later, err := s.applyPotential(ctx, r, func(n int) (int, error) { return n - 1, nil }, (*sql.Tx).Commit)
	if err != nil || later.Inventory.Character.HP != 60 || later.Inventory.Character.MP != 40 || later.Inventory.Character.BaseMaxHP != 100 || later.Inventory.Character.BaseMaxMP != 100 {
		t.Fatal("potential command healed or compounded expired maxima", later, err)
	}
}

func TestPotentialPGEpicFailureConsumesOnceAndRollbackFenceGuards(t *testing.T) {
	ctx, s, a := potentialCommandFixture(t)
	r := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialSeal, ClientTick: 94001, SourceSlot: 3, TargetSlot: -11}
	failed, err := s.applyPotential(ctx, r, func(n int) (int, error) { return n - 1, nil }, (*sql.Tx).Commit)
	if err != nil || failed.Status != 0 || potentialItemQuantity(failed.Inventory.Character, 3) != 1 || !equipmentAt(failed.Inventory.Character, -11).Potential.Present || equipmentAt(failed.Inventory.Character, -11).Potential.RawState != 0 {
		t.Fatal("reachable epic failure did not consume exactly one", failed, err)
	}
	a.Character = failed.Inventory.Character
	bad := r
	bad.Session = a
	bad.ClientTick++
	bad.Session.ChannelID = 2
	if _, err = s.ApplyPotential(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("wrong channel became owner", err)
	}
	bad.Session = a
	bad.Session.Generation++
	if _, err = s.ApplyPotential(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("wrong generation became owner", err)
	}
	bad.Session = a
	bad.Session.Character.HP = 0
	if _, err = s.ApplyPotential(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("dead actor spent material", err)
	}
	// A durable hidden preimage is explicit test setup for the fee denial.
	e := equipmentAt(a.Character, -11)
	e.Potential = domain.EquipmentPotential{Present: true, RawState: 1, Options: [5]uint16{65519}, Flags: 65535}
	a.Character.Equipment = append([]domain.Equipment(nil), a.Character.Equipment...)
	for i := range a.Character.Equipment {
		if a.Character.Equipment[i].ID == e.ID {
			a.Character.Equipment[i] = e
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistEquipmentMetadata(ctx, tx, a.Character); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET mesos=999 WHERE id=$1`, a.CharacterID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a = potentialReload(t, ctx, s, a)
	mag := domain.PotentialCommandRequest{Session: a, Action: domain.PotentialMagnify, ClientTick: 94003, SourceSlot: 4, TargetSlot: -11}
	if _, err = s.applyPotential(ctx, mag, func(int) (int, error) { t.Fatal("insufficient fee called RNG"); return 0, nil }, (*sql.Tx).Commit); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatal(err)
	}
	loaded := potentialReload(t, ctx, s, a)
	if loaded.Character.Mesos != 999 || potentialItemQuantity(loaded.Character, 4) != 2 || equipmentAt(loaded.Character, -11) != e {
		t.Fatal("denial changed fee/material/metadata")
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_meso_ledger WHERE character_id=$1`, a.CharacterID); n != 0 {
		t.Fatal("uncommitted fee ledger exists", n)
	}
}
