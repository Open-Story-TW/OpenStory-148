package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// Uses only the already-configured PG. Existing generated schema cleanup does
// not create/start another database/runtime. PG-down runs explicitly SKIP.
func itemRecoveryPGFixture(t *testing.T) (context.Context, *Store, domain.CharacterSession) {
	t.Helper()
	usePrivateSchema(t, "itemrecovery")
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, actors := inventoryFixture(t)
	r := t.TempDir()
	for path, data := range map[string]string{
		"Item.wz/Consume/0200.img.xml":         `<imgdir name="consume"><imgdir name="02000000"><imgdir name="spec"><int name="hp" value="50"/><int name="mp" value="20"/><int name="hpR" value="20"/><int name="mpR" value="10"/></imgdir></imgdir></imgdir>`,
		"Item.wz/ItemOption.img.xml":           `<imgdir name="ItemOption.img"><imgdir name="030551"><imgdir name="info"><int name="weight" value="5"/><int name="optionType" value="11"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryUP" value="10"/></imgdir></imgdir></imgdir><imgdir name="040551"><imgdir name="info"><int name="weight" value="5"/><int name="optionType" value="11"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryUP" value="20"/></imgdir></imgdir></imgdir></imgdir>`,
		"Character.wz/Weapon/01322005.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`,
		"Skill.wz/411.img.xml":                 `<imgdir name="book"><imgdir name="skill"><imgdir name="4110000"><imgdir name="common"><int name="maxLevel" value="20"/><string name="x" value="100+u(x*5)"/></imgdir></imgdir></imgdir></imgdir>`,
	} {
		p := filepath.Join(r, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", r)
	a := actors[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=411,level=100,base_max_hp=500,max_hp=500,hp=5,base_max_mp=100,max_mp=100,mp=1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,4110000,2,0)`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, a)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Equipment {
		if c.Equipment[i].Slot == -11 {
			c.Equipment[i].Potential = domain.EquipmentPotential{Present: true, RawState: 19, Options: [5]uint16{30551, 40551}}
		}
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, a
}

func TestItemRecoveryPGLostCommitReplyResolvesWithoutAnotherUse(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	q := domain.UseItemRequest{Session: a, ClientTick: 56001, Slot: 1, TemplateID: 2000000}
	commits := 0
	_, err := s.applyItemUse(ctx, q, func(tx *sql.Tx) error {
		commits++
		if e := tx.Commit(); e != nil {
			return e
		}
		return io.ErrUnexpectedEOF
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) || commits != 1 {
		t.Fatal("lost commit fixture", err, commits)
	}
	r, found, err := s.ResolveItemUse(ctx, q)
	if err != nil || !found || r.Character.HP != 175 || r.Character.MP != 31 || r.QuantityAfter != potentialItemQuantity(a.Character, 1)-1 {
		t.Fatal(r, found, err)
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_item_uses WHERE character_id=$1`, a.CharacterID); n != 1 {
		t.Fatal("resolve wrote another use", n)
	}
}

func TestItemRecoveryPGExpiredSavedPassiveUsesFreshSQLLevel(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	// Keep the live skill list stale. SQL expiry removes only the passive10;
	// both authoritative potential lines remain30 and do not multiply hpR/MP.
	if _, err := s.db.ExecContext(ctx, `UPDATE game_skills SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1 AND skill_id=4110000`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyItemUse(ctx, domain.UseItemRequest{Session: a, ClientTick: 57001, Slot: 1, TemplateID: 2000000})
	if err != nil || r.FixedHPPercent != 30 || r.Character.HP != 170 || r.Character.MP != 31 {
		t.Fatal("stale world passive consumed", r, err)
	}
}

func TestItemRecoveryPGAtomicBucketReplayResolveAndStaleCheckpointFence(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	q := domain.UseItemRequest{Session: a, ClientTick: 51001, Slot: 1, TemplateID: 2000000}
	oldQty := potentialItemQuantity(a.Character, 1)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.ApplyItemUse(ctx, q)
			if err != nil || r.Character.HP != 175 || r.Character.MP != 31 || potentialItemQuantity(r.Character, 1) != oldQty-1 {
				t.Errorf("same tick duplicate %+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	r, found, err := s.ResolveItemUse(ctx, q)
	if err != nil || !found || r.FixedHPPercent != 40 || r.Replayed || r.CommittedRevision != a.Character.Revision+1 {
		t.Fatal(r, found, err)
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_item_uses WHERE character_id=$1`, a.CharacterID); n != 1 {
		t.Fatal("double item receipt", n)
	}
	if err = s.SaveCharacter(ctx, a, a.Character); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("disconnect stale HP wrote over committed result", err)
	}
	// Read-only reconciliation still succeeds without today's catalog/content.
	t.Setenv("MAPLEGO_WZ_ROOT", filepath.Join(t.TempDir(), "not-mounted"))
	again, found, err := s.ResolveItemUse(ctx, q)
	if err != nil || !found || again.Character.HP != 175 || again.Character.MP != 31 {
		t.Fatal("resolve recalculated WZ", again, found, err)
	}
	bad := q
	bad.TemplateID = 2000001
	if _, _, err = s.ResolveItemUse(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("same tick swapped item", err)
	}
}

func TestItemRecoveryPGUnknownTierExpiryAndForeignOwnerRollback(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	before := a.Character
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := lockInventoryCharacter(ctx, tx, a)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Equipment {
		if c.Equipment[i].Slot == -11 {
			c.Equipment[i].Potential.RawState = 0x15
		}
	}
	// Persist opaque metadata directly, retaining the invalid native state, so
	// the consumer must reject before quantity/receipt mutation.
	if err = persistEquipmentMetadata(ctx, tx, c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	q := domain.UseItemRequest{Session: a, ClientTick: 52001, Slot: 1, TemplateID: 2000000}
	if _, err = s.ApplyItemUse(ctx, q); err == nil {
		t.Fatal("unknown potential tier consumed potion")
	}
	var qty int
	if err = s.db.QueryRowContext(ctx, `SELECT quantity FROM asset_items WHERE character_id=$1 AND inv_type=2 AND slot=1`, a.CharacterID).Scan(&qty); err != nil || qty != potentialItemQuantity(before, 1) {
		t.Fatal(qty, err)
	}
	if n := potentialCount(t, ctx, s, `SELECT count(*) FROM asset_item_uses WHERE character_id=$1`, a.CharacterID); n != 0 {
		t.Fatal("denied command committed receipt")
	}
	q.Session.Generation++
	if _, err = s.ApplyItemUse(ctx, q); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale owner", err)
	}
}

func TestItemRecoveryPGExpiredHyperBodyClampsBothVitalsAndKeepsBase(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET base_max_hp=100,base_max_mp=100,max_hp=160,max_mp=160,hp=150,mp=150 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, stat := range []string{"MAXHP", "MAXMP"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at) VALUES($1,1301007,20,$2,60,clock_timestamp()+interval '1 hour')`, a.CharacterID, stat); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil || a.Character.MaxHP != 160 || a.Character.MaxMP != 160 {
		t.Fatal(a.Character, err)
	}
	// The world snapshot predates expiry, as when it waited behind SQL work.
	if _, err = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyItemUse(ctx, domain.UseItemRequest{Session: a, ClientTick: 53001, Slot: 1, TemplateID: 2000000})
	if err != nil || r.Character.HP != 100 || r.Character.MP != 100 || r.Character.MaxHP != 100 || r.Character.MaxMP != 100 || r.Character.BaseMaxHP != 100 || r.Character.BaseMaxMP != 100 {
		t.Fatal("expired cap healed or changed base", r, err)
	}
}

func TestItemRecoveryPGLegacyCorruptAndLaterRevisionReceiptsDoNotResolve(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	q := domain.UseItemRequest{Session: a, ClientTick: 54001, Slot: 1, TemplateID: 2000000}
	r, err := s.ApplyItemUse(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	for _, column := range []string{"result_bytes", "result_sha256"} {
		if _, e := s.db.ExecContext(ctx, `UPDATE asset_item_uses SET `+column+`=NULL WHERE character_id=$1`, a.CharacterID); e == nil {
			t.Fatal("mixed NULL receipt passed CHECK", column)
		}
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_uses SET result_bytes=NULL,result_sha256=NULL WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, found, e := s.ResolveItemUse(ctx, q); !found || !errors.Is(e, domain.ErrLegacyItemUseReceipt) {
		t.Fatal(found, e)
	}
	r.QuantityAfter = 99
	bad, _ := json.Marshal(r)
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_uses SET result_bytes=$1,result_sha256=$2 WHERE character_id=$3`, bad, itemUseDigest(bad), a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, _, e := s.ResolveItemUse(ctx, q); !errors.Is(e, domain.ErrItemUseReceiptInvalid) {
		t.Fatal("corrupt delta accepted", e)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_item_uses SET result_bytes=$1,result_sha256=$2 WHERE character_id=$3`, raw, itemUseDigest(raw), a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a.Character = r.Character
	a.Character.HP = 20
	if err = s.SaveCharacter(ctx, a, a.Character); err != nil {
		t.Fatal(err)
	}
	if _, _, e := s.ResolveItemUse(ctx, q); !errors.Is(e, domain.ErrStaleOwner) {
		t.Fatal("old full receipt could overwrite later damage", e)
	}
}

func TestItemRecoveryPGResolveWaitsForOriginalIdentityLockBeforeAbsence(t *testing.T) {
	ctx, s, a := itemRecoveryPGFixture(t)
	q := domain.UseItemRequest{Session: a, ClientTick: 55001, Slot: 1, TemplateID: 2000000}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = lockItemUseIdentity(ctx, tx, q); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		found bool
		err   error
	}
	done := make(chan answer, 1)
	go func() { _, found, e := s.ResolveItemUse(ctx, q); done <- answer{found, e} }()
	select {
	case got := <-done:
		t.Fatal("absence declared before first transaction ended", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.found {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("resolver did not release")
	}
}
