package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Existing configured PG only. No database service or independent runtime is
// provisioned. These tests are skipped while the integration flag/PG is absent.
func resourcePGFixture(t *testing.T, job int) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	usePrivateSchema(t, "resourcecost")
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, actors := inventoryFixture(t)
	t.Setenv("MAPLEGO_WZ_ROOT", writeResourceCommandWZ(t))
	for i := range actors {
		if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=$2,level=150,base_max_hp=100,max_hp=100,hp=50,base_max_mp=100,max_mp=100,mp=100,map_id=40000,x=0,y=0 WHERE id=$1`, actors[i].CharacterID, job); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int{1001003, 1001004, 1001005, 1000, 2110001, 2121003, 1301007} {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,$2,CASE WHEN $2=1301007 THEN 20 ELSE 1 END,0)`, actors[i].CharacterID, id); err != nil {
				t.Fatal(err)
			}
		}
		var err error
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Explicit test-only persisted potential; never production/MCP funding.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, actors[0])
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Equipment {
		if c.Equipment[i].Slot == -11 {
			c.Equipment[i].Potential = domain.EquipmentPotential{Present: true, RawState: 20, Options: [5]uint16{40502}}
		}
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	actors[0].Character = c
	return ctx, s, actors
}

func TestResourcePGSelfCastAtomicDebitCooldownReplayRollback(t *testing.T) {
	ctx, s, actors := resourcePGFixture(t, 100)
	a := actors[0]
	q := domain.SkillCastRequest{Session: a, SkillID: 1001003, ClientLevel: 1, OperationID: "resource-self-concurrent-cast"}
	started := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.CastSkill(ctx, q); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if c.MP != 95 || len(c.SkillCooldowns) != 1 || c.SkillCooldowns[0].ExpiresAt.Before(started.Add(3*time.Second-100*time.Millisecond)) || c.SkillCooldowns[0].ExpiresAt.After(time.Now().Add(3*time.Second+100*time.Millisecond)) {
		t.Fatal("self debit/cooldown", c.MP, c.SkillCooldowns)
	}
	r, err := s.CastSkill(ctx, q)
	if err != nil || !r.Replayed || r.Character.MP != 95 {
		t.Fatal("same op recharged", r, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_skill_cooldowns SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	before := a.Character
	if _, err = s.db.ExecContext(ctx, `CREATE FUNCTION resource_self_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='resource-self-forced-rollback' THEN RAISE EXCEPTION 'test rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER resource_self_fail BEFORE INSERT ON asset_skill_commands FOR EACH ROW EXECUTE FUNCTION resource_self_fail()`); err != nil {
		t.Fatal(err)
	}
	q.Session = a
	q.OperationID = "resource-self-forced-rollback"
	if _, err = s.CastSkill(ctx, q); err == nil {
		t.Fatal("receipt failure committed")
	}
	after, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || after.MP != before.MP || after.Revision != before.Revision || len(after.SkillBuffs) != len(before.SkillBuffs) {
		t.Fatal("partial self rollback", after.MP, err)
	}
}

func TestResourcePGPartyCastChargesCasterOnceAndResolves(t *testing.T) {
	ctx, s, actors := resourcePGFixture(t, 130)
	settlementParty(t, ctx, s, actors)
	q := hyperBodyRequest(t, ctx, s, actors, "resource-party-one-atomic-cast", 77)
	r, err := s.ApplyPartySkillCast(ctx, q)
	if err != nil || len(r.Recipients) != 2 {
		t.Fatal(r, err)
	}
	for _, p := range r.Recipients {
		want := 100
		if p.Character.ID == actors[0].CharacterID {
			want = 95
		}
		if p.Character.MP != want {
			t.Fatal("recipient paid caster fee", p.Character.ID, p.Character.MP)
		}
	}
	hash, err := domain.PartySkillManifestHash(q)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.PartySkillCastOutcome(ctx, q.Session.Lease, q.OperationID, hash)
	if err != nil || out == nil || !out.Replayed {
		t.Fatal("lost reply unresolved", out, err)
	}
	if _, err = s.ApplyPartySkillCast(ctx, q); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetCharacter(ctx, actors[0].CharacterID)
	if err != nil || c.MP != 95 || len(c.SkillCooldowns) != 1 {
		t.Fatal("party recharged", c.MP, err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM game_skill_cooldowns WHERE character_id=$1`, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM game_skill_buffs WHERE character_id IN($1,$2)`, actors[0].CharacterID, actors[1].CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET mp=4 WHERE id=$1`, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	for i := range actors {
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
	}
	q = hyperBodyRequest(t, ctx, s, actors, "resource-party-insufficient-cast", 78)
	if _, err = s.ApplyPartySkillCast(ctx, q); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatal("insufficient modified party", err)
	}
	for i, a := range actors {
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil || c.MP != a.Character.MP || c.Revision != a.Character.Revision || len(c.SkillBuffs) != 0 {
			t.Fatal("partial party failure", i, c, err)
		}
	}
}

func TestResourcePGGenericAtomicCostTamperAndOutcome(t *testing.T) {
	ctx, s, actors := resourcePGFixture(t, 212)
	a := actors[0]
	q := domain.CombatAttackRequest{Session: a, Members: []domain.CharacterSession{a}, OperationID: "resource-generic-original-once", ClientTick: 70, Family: domain.CombatSkill, SkillID: 2121003, LinesPerTarget: 1, Cost: &domain.CombatCost{Opcode: protocol.RecvMagicAttack, MP: 7, Level: 1, CooldownMS: 3000}}
	hash, err := domain.CombatManifestHash(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyCombatAttack(ctx, q); err != nil {
		t.Fatal(err)
	}
	out, err := s.CombatAttackOutcome(ctx, a.Lease, q.OperationID, hash)
	if err != nil || out == nil || !out.Replayed {
		t.Fatal("unresolved outcome", out, err)
	}
	if _, err = s.ApplyCombatAttack(ctx, q); err != nil {
		t.Fatal("replay", err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil || a.Character.MP != 93 {
		t.Fatal("generic duplicate debit", a.Character.MP, err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM game_skill_cooldowns WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for i, mp := range []int{0, 6, 8} {
		bad := q
		bad.Session = a
		bad.Members = []domain.CharacterSession{a}
		bad.OperationID = fmt.Sprintf("resource-generic-bad-cost-%d", i)
		bad.ClientTick = uint32(71 + i)
		cost := *q.Cost
		cost.MP = mp
		bad.Cost = &cost
		if _, err = s.ApplyCombatAttack(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("wrong cost committed", mp, err)
		}
	}
	c, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || c.MP != 93 || c.Revision != a.Character.Revision {
		t.Fatal("tamper changed assets", c.MP, err)
	}
}

func TestResourcePGLegacyFamiliesShareCostAndReplay(t *testing.T) {
	ctx, s, actors := resourcePGFixture(t, 100)
	a := actors[0]
	for i, tc := range []struct {
		id, mp int
		family domain.CombatFamily
	}{{0, 0, domain.CombatNormal}, {1001004, 4, domain.CombatPhysicalSkill}, {1001005, 5, domain.CombatPhysicalSkill}, {1000, 3, domain.CombatThreeSnails}} {
		q := domain.CombatAttackRequest{Session: a, Members: []domain.CharacterSession{a}, OperationID: fmt.Sprintf("resource-legacy-family-%d", i), ClientTick: uint32(80 + i), Family: tc.family, SkillID: tc.id}
		// Legacy normal/physical commands use aggregate damage; only Three
		// Snails requires a per-target line manifest.
		if tc.family == domain.CombatThreeSnails {
			q.LinesPerTarget = 1
		}
		if _, err := s.ApplyCombatAttack(ctx, q); err != nil {
			t.Fatal(tc, err)
		}
		if r, err := s.ApplyCombatAttack(ctx, q); err != nil || !r.Replayed {
			t.Fatal("legacy replay", tc, err)
		}
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil || c.MP != a.Character.MP-tc.mp {
			t.Fatal("legacy wrong debit", tc, c.MP, err)
		}
		a.Character = c
	}
}
