package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestProfessionExchangeRulesLevelsAndReaders(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	n := 0
	run := func(p domain.ProfessionChange, items ...domain.ScriptItemDelta) (domain.ScriptExchangeResult, error) {
		t.Helper()
		n++
		r, err := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: a, OperationID: fmt.Sprintf("profession-rules-%d-%d", a.CharacterID, n), Items: items, Profession: &p})
		if err == nil {
			a.Character = r.Character
		}
		return r, err
	}
	refused := func(what string, p domain.ProfessionChange) {
		t.Helper()
		before := a.Character.Revision
		if _, err := run(p); !errors.Is(err, domain.ErrInsufficient) {
			t.Fatalf("%s: %v", what, err)
		}
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil || c.Revision != before {
			t.Fatalf("%s changed the character: %v", what, err)
		}
	}
	refused("a craft without its gathering profession", domain.ProfessionChange{Learn: content.ProfessionAlchemy})
	refused("proficiency for an unlearned profession", domain.ProfessionChange{Gain: content.ProfessionHerbalism, EXP: 10})
	r, err := run(domain.ProfessionChange{Learn: content.ProfessionHerbalism}, domain.ScriptItemDelta{TemplateID: 2000000, Quantity: 1})
	if err != nil || r.Character.Profession(content.ProfessionHerbalism) != (domain.ProfessionState{ID: content.ProfessionHerbalism, Level: 1}) || len(r.Changes) == 0 {
		t.Fatalf("learn herbalism: %+v %v", r.Character.Professions, err)
	}
	refused("learning twice", domain.ProfessionChange{Learn: content.ProfessionHerbalism})
	refused("a second gathering profession", domain.ProfessionChange{Learn: content.ProfessionMining})
	refused("a craft of the other gathering profession", domain.ProfessionChange{Learn: content.ProfessionSmithing})
	if _, err = run(domain.ProfessionChange{Learn: content.ProfessionAlchemy}); err != nil {
		t.Fatal(err)
	}
	refused("dropping a gathering profession under its craft", domain.ProfessionChange{Forget: content.ProfessionHerbalism})
	refused("a recipe of an unlearned profession", domain.ProfessionChange{Recipe: 92020031})
	if r, err = run(domain.ProfessionChange{Recipe: 92040011}); err != nil || !r.Character.KnowsRecipe(92040011) {
		t.Fatalf("learn recipe: %v", err)
	}
	refused("learning a recipe twice", domain.ProfessionChange{Recipe: 92040011})

	// 250 proficiency ends level 1; the rest carries into level 2.
	r, err = run(domain.ProfessionChange{Gain: content.ProfessionHerbalism, EXP: 260, Fatigue: 150, Traits: domain.Traits{Sense: 5}})
	if err != nil || r.Character.Profession(content.ProfessionHerbalism) != (domain.ProfessionState{ID: content.ProfessionHerbalism, Level: 2, EXP: 10}) || r.Character.Fatigue != 150 || r.Character.Traits.Sense != 5 {
		t.Fatalf("gain: %+v fatigue=%d traits=%+v %v", r.Character.Professions, r.Character.Fatigue, r.Character.Traits, err)
	}
	refused("work past the fatigue limit", domain.ProfessionChange{Gain: content.ProfessionHerbalism, EXP: 1, Fatigue: 51})
	if r, err = run(domain.ProfessionChange{Fatigue: 50, Traits: domain.Traits{Sense: 3, Craft: 2}}); err != nil || r.Character.Fatigue != content.ProfessionFatigueLimit || r.Character.Traits != (domain.Traits{Sense: 8, Craft: 2}) {
		t.Fatalf("fatigue at the limit: %+v %v", r.Character, err)
	}

	// Both loaders agree, and the roster projection carries no profession rows.
	for _, viaSQL := range []bool{false, true} {
		var c domain.Character
		if viaSQL {
			c, err = readCharacterSQL(ctx, s.db, a.CharacterID)
		} else {
			c, err = s.GetCharacter(ctx, a.CharacterID)
		}
		if err != nil || len(c.Professions) != 2 || c.Profession(content.ProfessionAlchemy).Level != 1 || c.Profession(content.ProfessionHerbalism).EXP != 10 || !c.KnowsRecipe(92040011) || c.Fatigue != content.ProfessionFatigueLimit || c.Traits.Sense != 8 {
			t.Fatalf("reader sql=%v: %+v %v", viaSQL, c, err)
		}
	}
	// Fatigue wears off one point per professionFatigueSeconds.
	if _, err = s.db.ExecContext(ctx, `UPDATE game_character_traits SET fatigue_at=fatigue_at-make_interval(secs=>$1) WHERE character_id=$2`, 10*professionFatigueSeconds+5, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if c, err := s.GetCharacter(ctx, a.CharacterID); err != nil || c.Fatigue != content.ProfessionFatigueLimit-10 {
		t.Fatalf("fatigue after rest: %d %v", c.Fatigue, err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}

	// Dropping the craft forgets its recipes; then the gathering one can go.
	if r, err = run(domain.ProfessionChange{Forget: content.ProfessionAlchemy}); err != nil || r.Character.KnowsRecipe(92040011) || len(r.Character.Professions) != 1 {
		t.Fatalf("forget craft: %+v %v", r.Character.Professions, err)
	}
	if r, err = run(domain.ProfessionChange{Forget: content.ProfessionHerbalism}); err != nil || len(r.Character.Professions) != 0 {
		t.Fatalf("forget gathering: %+v %v", r.Character.Professions, err)
	}
	var rows int
	if err = s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM game_professions WHERE character_id=$1)+(SELECT count(*) FROM game_profession_recipes WHERE character_id=$1)`, a.CharacterID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("profession rows left: %d %v", rows, err)
	}

	// A replayed exchange returns its receipt and changes nothing.
	op := domain.ScriptExchange{Session: a, OperationID: fmt.Sprintf("profession-replay-%d", a.CharacterID), Profession: &domain.ProfessionChange{Learn: content.ProfessionMining}}
	first, err := s.ExecuteScriptExchange(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ExecuteScriptExchange(ctx, op)
	if err != nil || !again.Replayed || again.Character.Revision != first.Character.Revision || again.Character.Profession(content.ProfessionMining).Level != 1 {
		t.Fatalf("replay: %+v %v", again.Character.Professions, err)
	}
	a.Character = first.Character
	if _, err = s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: a, OperationID: fmt.Sprintf("profession-invalid-%d", a.CharacterID), Profession: &domain.ProfessionChange{Learn: 1000}}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unknown profession: %v", err)
	}
}
