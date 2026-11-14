package persistence

import (
	"context"
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func hyperBodyFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	ctx, s, actors := settlementFixture(t)
	for i := range actors {
		job, baseHP, baseMP := 130, 411, 100
		if i == 1 {
			job, baseHP, baseMP = 200, 100, 101
		}
		if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET job=$2,level=30,base_max_hp=$3,base_max_mp=$4,hp=100,mp=100 WHERE id=$1`, actors[i].CharacterID, job, baseHP, baseMP); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,1301006,3,0),($1,1301007,20,0),($1,1000006,10,0)`, actors[i].CharacterID); e != nil {
				t.Fatal(e)
			}
		} else {
			if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,2000006,10,0)`, actors[i].CharacterID); e != nil {
				t.Fatal(e)
			}
		}
		var e error
		actors[i].Character, e = s.GetCharacter(ctx, actors[i].CharacterID)
		if e != nil {
			t.Fatal(e)
		}
	}
	settlementParty(t, ctx, s, actors)
	return ctx, s, actors
}
func hyperBodyRequest(t *testing.T, ctx context.Context, s *Store, actors []domain.CharacterSession, op string, tick uint32) domain.PartySkillCastRequest {
	t.Helper()
	state, e := s.ReadParty(ctx, actors[0])
	if e != nil {
		t.Fatal(e)
	}
	q := domain.PartySkillCastRequest{Session: actors[0], OperationID: op, ClientTick: tick, SkillID: 1301007, ClientLevel: 20, PartyID: state.Party.ID, PartyRevision: state.Party.Revision, Members: append([]domain.CharacterSession(nil), actors...)}
	for _, p := range state.Party.Members {
		q.PartyMembers = append(q.PartyMembers, p.CharacterID)
	}
	return q
}
func TestHyperBodyAtomicPartyCastReplayRollbackAndExpiry(t *testing.T) {
	ctx, s, actors := hyperBodyFixture(t)
	actors[1].Character.X, actors[1].Character.Y = 300, 200
	q := hyperBodyRequest(t, ctx, s, actors, "hyper-body-party-initial", 1)
	r, e := s.ApplyPartySkillCast(ctx, q)
	if e != nil || len(r.Recipients) != 2 {
		t.Fatalf("party cast:%+v %v", r, e)
	}
	for _, p := range r.Recipients {
		c := p.Character
		if c.HP != 100 || c.BaseMaxHP != p.Session.Character.BaseMaxHP || c.BaseMaxMP != p.Session.Character.BaseMaxMP || c.Revision != p.Session.Character.Revision+1 || len(c.SkillBuffs) != 2 {
			t.Fatal("cast healed, changed base or lost atomic revision")
		}
		if c.ID == actors[0].CharacterID {
			if c.MaxHP != 739 || c.MaxMP != 160 || c.MP != 40 {
				t.Fatalf("caster additive passive/MP:%+v", c)
			}
		} else {
			if c.MaxHP != 160 || c.MaxMP != 181 || c.MP != 100 {
				t.Fatalf("recipient mage passive:%+v", c)
			}
		}
	}
	replay, e := s.ApplyPartySkillCast(ctx, q)
	if e != nil || !replay.Replayed || len(replay.Recipients) != 2 {
		t.Fatal("replay", e)
	}
	hash, _ := domain.PartySkillManifestHash(q)
	out, e := s.PartySkillCastOutcome(ctx, q.Session.Lease, q.OperationID, hash)
	if e != nil || out == nil || out.OperationID != q.OperationID || !out.Replayed {
		t.Fatal("same-op outcome", e)
	}
	forged := q
	forged.OperationID = "hyper-body-renamed-tick"
	if _, e = s.ApplyPartySkillCast(ctx, forged); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("same native tick was charged twice", e)
	}
	// Real failure after all recipient rows/rosters are written must roll back all.
	if _, e = s.db.ExecContext(ctx, `UPDATE game_characters SET mp=100 WHERE id=$1`, actors[0].CharacterID); e != nil {
		t.Fatal(e)
	}
	for i := range actors {
		actors[i].Character, e = s.GetCharacter(ctx, actors[i].CharacterID)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.db.ExecContext(ctx, `CREATE FUNCTION hyper_body_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='hyper-body-forced-rollback' THEN RAISE EXCEPTION 'intentional rollback'; END IF; RETURN NEW; END $$;CREATE TRIGGER hyper_body_fail BEFORE INSERT ON asset_skill_casts FOR EACH ROW EXECUTE FUNCTION hyper_body_fail()`); e != nil {
		t.Fatal(e)
	}
	fail := hyperBodyRequest(t, ctx, s, actors, "hyper-body-forced-rollback", 2)
	if _, e = s.ApplyPartySkillCast(ctx, fail); e == nil {
		t.Fatal("failed receipt insert committed")
	}
	for _, a := range actors {
		c, e := s.GetCharacter(ctx, a.CharacterID)
		if e != nil || c.Revision != a.Character.Revision || c.MP != a.Character.MP || !c.SkillBuffs[0].ExpiresAt.Equal(a.Character.SkillBuffs[0].ExpiresAt) {
			t.Fatal("partial party rollback", e)
		}
	}
	// A still-live high-HP snapshot must expire and clamp, not fail validation.
	for i := range actors {
		c := actors[i].Character
		c.HP, c.MP = c.MaxHP, c.MaxMP
		actors[i].Character = c
		if _, e = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=$2 WHERE character_id=$1`, c.ID, time.Now().Add(-time.Second)); e != nil {
			t.Fatal(e)
		}
		tick, e := s.TickSkillBuffs(ctx, actors[i])
		if e != nil || len(tick.Expired) != 2 || len(tick.Character.SkillBuffs) != 0 || tick.Character.HP != tick.Character.MaxHP || tick.Character.MP != tick.Character.MaxMP {
			t.Fatalf("expired high-current maxima:%+v %v", tick, e)
		}
		if tick.Character.BaseMaxHP != c.BaseMaxHP || tick.Character.BaseMaxMP != c.BaseMaxMP {
			t.Fatal("expiry modified base")
		}
	}
}
func TestHyperBodyPartyRevisionRangeAndDeadRecipients(t *testing.T) {
	for _, kind := range []string{"range", "dead", "party_revision", "owner", "unlearned"} {
		t.Run(kind, func(t *testing.T) {
			ctx, s, actors := hyperBodyFixture(t)
			q := hyperBodyRequest(t, ctx, s, actors, "hyper-body-eligibility-"+kind, 4)
			switch kind {
			case "range":
				q.Members[1].Character.X = 301
			case "dead":
				q.Members[1].Character.HP = 0
			case "party_revision":
				q.PartyRevision++
			case "owner":
				q.Members[1].Generation++
			case "unlearned":
				if _, e := s.db.ExecContext(ctx, `UPDATE game_skills SET expires_at=$2 WHERE character_id=$1 AND skill_id=1301006`, actors[0].CharacterID, time.Now().Add(-time.Second)); e != nil {
					t.Fatal(e)
				}
			}
			r, e := s.ApplyPartySkillCast(ctx, q)
			if kind == "range" || kind == "dead" {
				if e != nil || len(r.Recipients) != 1 || r.Recipients[0].Character.ID != actors[0].CharacterID {
					t.Fatal("ineligible peer affected", e)
				}
			} else {
				if e == nil {
					t.Fatal("invalid party/owner/prerequisite accepted")
				}
				for _, a := range actors {
					c, err := s.GetCharacter(ctx, a.CharacterID)
					if err != nil || c.MP != 100 || len(c.SkillBuffs) != 0 {
						t.Fatal("rejection partially charged/effected", err)
					}
				}
			}
		})
	}
}

func TestHyperBodyEarnedSPPrerequisiteAndLevelOneCast(t *testing.T) {
	ctx, s, actors := hyperBodyFixture(t)
	id := actors[0].CharacterID
	if _, e := s.db.ExecContext(ctx, `DELETE FROM game_skills WHERE character_id=$1 AND skill_id IN(1301006,1301007)`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET sp=4 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	var e error
	actors[0].Character, e = s.GetCharacter(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	for level := 1; level <= 3; level++ {
		r, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1301006, OperationID: fmt.Sprintf("hyper-body-prerequisite-%d", level)})
		if e != nil || r.Character.SP != 4-level || r.Character.HP != 100 {
			t.Fatal("earned prerequisite SP", e)
		}
		actors[0].Character = r.Character
	}
	r, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1301007, OperationID: "hyper-body-earned-first-point"})
	if e != nil || r.Character.SP != 0 || learned(r.Character, 1301007).Level != 1 {
		t.Fatal("earned Hyper Body SP", e)
	}
	actors[0].Character = r.Character
	q := hyperBodyRequest(t, ctx, s, actors, "hyper-body-level-one-cast", 8)
	q.ClientLevel = 1
	cast, e := s.ApplyPartySkillCast(ctx, q)
	if e != nil || len(cast.Recipients) != 2 {
		t.Fatal("earned level1 cast", e)
	}
	for _, p := range cast.Recipients {
		if p.Character.ID == id && (p.Character.MP != 80 || p.Character.MaxHP != 505 || p.Character.MaxMP != 103 || p.Character.SP != 0) {
			t.Fatal("level1 source cost/stats mismatch")
		}
	}
}

func TestHyperBodyRecipientCancellationIsLocalAndClampsCurrentVitals(t *testing.T) {
	ctx, s, actors := hyperBodyFixture(t)
	q := hyperBodyRequest(t, ctx, s, actors, "hyper-body-self-cancellation", 9)
	cast, e := s.ApplyPartySkillCast(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range cast.Recipients {
		for i := range actors {
			if actors[i].CharacterID == row.Character.ID {
				actors[i].Character = row.Character
			}
		}
	}
	recipient := actors[1]
	recipient.Character.HP, recipient.Character.MP = 160, 181
	cancel, e := s.CancelSkill(ctx, recipient, 1301007)
	if e != nil || len(cancel.Expired) != 2 || cancel.Character.MaxHP != 100 || cancel.Character.MaxMP != 121 || cancel.Character.HP != 100 || cancel.Character.MP != 121 || len(cancel.Character.SkillBuffs) != 0 {
		t.Fatalf("local cancel/clamp:%+v %v", cancel, e)
	}
	caster, e := s.GetCharacter(ctx, actors[0].CharacterID)
	if e != nil || len(caster.SkillBuffs) != 2 || caster.MaxHP != 739 || caster.MP != 40 {
		t.Fatal("recipient cancel propagated to caster", e)
	}
}
