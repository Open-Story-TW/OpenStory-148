package persistence

import (
	"context"
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

// The world tests prove source/tick/scene admission. This integration case
// proves only that the resulting dirty runtime vitals checkpoint atomically;
// it is not a claim that PostgreSQL reconstructs/replays native recovery.
func TestRegenerationRuntimeCheckpointReloadAndFencedReplay(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET hp=2,mp=1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	value := a.Character
	value.HP = min(value.MaxHP, value.HP+10)
	value.MP = min(value.MaxMP, value.MP+3)
	if err = s.SaveCharacter(ctx, a, value); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveCharacter(ctx, a, value); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("old checkpoint replay not fenced", err)
	}
	sql, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || sql.HP != value.HP || sql.MP != value.MP || ent.HP != value.HP || ent.MP != value.MP || sql.BaseMaxHP != a.Character.BaseMaxHP || sql.BaseMaxMP != a.Character.BaseMaxMP {
		t.Fatal("recovery checkpoint lost base/vitals", sql, ent, err)
	}
}
func TestRegenerationRuntimeCheckpointLateFailureRollsBack(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET hp=2,mp=1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("regen_checkpoint_%d", a.CharacterID)
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'intentional recovery roster failure'; END $$; CREATE TRIGGER %s BEFORE UPDATE ON game_character_roster FOR EACH ROW WHEN (NEW.character_id=%d) EXECUTE FUNCTION %s()`, name, name, a.CharacterID, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = s.db.ExecContext(c, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON game_character_roster; DROP FUNCTION IF EXISTS %s()`, name, name))
	})
	value := a.Character
	value.HP = min(value.MaxHP, value.HP+10)
	value.MP = min(value.MaxMP, value.MP+3)
	if err = s.SaveCharacter(ctx, a, value); err == nil {
		t.Fatal("late checkpoint failure hidden")
	}
	got, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || got.HP != a.Character.HP || got.MP != a.Character.MP || got.Revision != a.Character.Revision {
		t.Fatal("partial recovery checkpoint survived rollback", got, err)
	}
}
