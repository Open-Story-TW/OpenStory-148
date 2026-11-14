package persistence

import (
	"errors"
	"os"
	"testing"

	"maple-go-server/internal/domain"
)

func TestQAFamilySwitchesOfflineQACharacterWithStarterLook(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	ctx, s, actors := qaGMFixture(t)
	if root == "" {
		t.Skip("family gear needs MAPLEGO_WZ_ROOT")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	qa := grantQAActor(t, ctx, s, actors[0])
	plain := actors[1]
	for _, a := range []domain.CharacterSession{qa, plain} {
		if _, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline' WHERE account_id=$1`, a.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	req := domain.QAFamilyRequest{OperationID: "qa-family-mercedes-0001", Actor: "unit-test", AccountID: qa.AccountID, CharacterID: qa.CharacterID, Job: 2310, Reason: "isolated schema automated test"}
	if _, err := s.ApplyQAFamily(ctx, domain.QAFamilyRequest{OperationID: "qa-family-plain-0001", Actor: "unit-test", AccountID: plain.AccountID, CharacterID: plain.CharacterID, Job: 2310, Reason: "no grant"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("character without QA grant switched: %v", err)
	}
	r, err := s.ApplyQAFamily(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Character
	if c.Job != 2310 || c.Face != 20549 || c.Hair != 33453 || equipmentAt(c, -5).ItemID != 1050192 || equipmentAt(c, -6).ItemID != 0 || equipmentAt(c, -7).ItemID != 1072519 || equipmentAt(c, -10).ItemID != 1352000 || equipmentAt(c, -11).ItemID != 1522038 {
		t.Fatalf("mercedes look %+v", c)
	}
	if equipmentAt(c, -11).WATK == 0 || equipmentAt(c, 1).ItemID == 0 || equipmentAt(c, 4).ItemID == 0 {
		t.Fatalf("WZ stats or displaced gear lost %+v", c.Equipment)
	}
	if again, err := s.ApplyQAFamily(ctx, req); err != nil || !again.Replayed || again.Character.Revision != c.Revision {
		t.Fatalf("replay %+v %v", again, err)
	}
	req.Job = 3100
	if _, err = s.ApplyQAFamily(ctx, req); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	req.OperationID = "qa-family-demon-0002"
	if r, err = s.ApplyQAFamily(ctx, req); err != nil || r.Character.Job != 3100 || r.Character.DemonMark != 1012276 || equipmentAt(r.Character, -10).ItemID != 1099001 || equipmentAt(r.Character, -11).ItemID != 1322123 {
		t.Fatalf("demon %+v %v", r.Character, err)
	}
	loaded, err := s.GetCharacter(ctx, qa.CharacterID)
	if err != nil || loaded.DemonMark != 1012276 || loaded.Job != 3100 || loaded.Face != 20248 {
		t.Fatalf("reload %+v %v", loaded, err)
	}
	req.OperationID, req.Job = "qa-family-phantom-0003", 2412
	if r, err = s.ApplyQAFamily(ctx, req); err != nil || r.Character.Job != 2412 || r.Character.DemonMark != 0 || r.Character.Face != 20248 {
		t.Fatalf("phantom keeps look %+v %v", r.Character, err)
	}
	var audits int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM ops_qa_family_changes WHERE character_id=$1`, qa.CharacterID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audits %d %v", audits, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='world' WHERE account_id=$1`, qa.AccountID); err != nil {
		t.Fatal(err)
	}
	req.OperationID = "qa-family-online-0004"
	if _, err = s.ApplyQAFamily(ctx, req); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("online character switched: %v", err)
	}
}

func TestQAItemGrantsWeaponsProjectilesAndRocks(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	ctx, s, actors := qaGMFixture(t)
	if root == "" {
		t.Skip("item templates load from MAPLEGO_WZ_ROOT")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	qa := grantQAActor(t, ctx, s, actors[0])
	run := func(op string, args ...int64) domain.GMCommandResult {
		t.Helper()
		r, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: qa, OperationID: "qa-item-" + op + "-0001", Name: "item", Args: args})
		if err == nil {
			qa.Character = r.Character
		}
		return r
	}
	if r := run("claw", 1472000); r.Denied || len(r.Inventory) != 1 || r.Inventory[0].Equipment == nil || r.Inventory[0].Equipment.ItemID != 1472000 || r.Inventory[0].Equipment.WATK == 0 {
		t.Fatalf("claw %+v", r)
	}
	if r := run("stars", 2070000, 500); r.Denied || len(r.Inventory) == 0 || r.Inventory[0].Item == nil || r.Inventory[0].Item.TemplateID != 2070000 {
		t.Fatalf("stars %+v", r)
	}
	if r := run("rocks", 4006001, 50); r.Denied || len(r.Inventory) != 1 || r.Inventory[0].Item.Quantity != 50 {
		t.Fatalf("summoning rocks %+v", r)
	}
	for op, args := range map[string][]int64{"cash": {5000000, 1}, "two-claws": {1472000, 2}, "unknown": {1479999}, "toomany": {4006001, 1001}} {
		if r := run(op, args...); !r.Denied {
			t.Fatalf("%s accepted %+v", op, r)
		}
	}
	plain := actors[1]
	if _, err := s.ExecuteGMCommand(ctx, domain.GMCommandRequest{Session: plain, OperationID: "qa-item-plain-0001", Name: "item", Args: []int64{1472000}}); err == nil {
		t.Fatal("character without QA grant received an item")
	}
}
