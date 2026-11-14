package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestPhysicalSkillAtomicMultiTargetReceiptsAndRollback(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=10,str=50,dex=4,sp=3,mp=100,base_max_mp=100,max_mp=100 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1001004, 1001005} {
		r, e := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: id, OperationID: fmt.Sprintf("physical-allocate-%d-%d", a.CharacterID, id)})
		if e != nil {
			t.Fatal(e)
		}
		a.Character = r.Character
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mob, err := catalog.Mob(9300018)
	if err != nil {
		t.Fatal(err)
	}
	q := domain.PhysicalAttackRequest{Session: a, OperationID: fmt.Sprintf("physical-multi-operation-%d", a.CharacterID), ClientTick: 100, SkillID: 1001005}
	for i := 0; i < 2; i++ {
		q.Targets = append(q.Targets, domain.PhysicalAttackTarget{ObjectID: int32(1000001 + i), MobID: mob.ID, SpawnOperationID: fmt.Sprintf("mob-physical-spawn-%d-%d", a.CharacterID, i), X: 0, Y: 0, Damage: 1, Defeated: true, Experience: mob.EXP})
	}
	r, err := s.ApplyPhysicalSkillAttack(ctx, q)
	if err != nil || r.Character.MP != 94 || r.Character.EXP != 2*mob.EXP {
		t.Fatalf("multi target result %+v %v", r, err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs WHERE owner_id=$1`, a.CharacterID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("durable per-kill loot jobs=%d %v", count, err)
	}
	replay, err := s.ApplyPhysicalSkillAttack(ctx, q)
	if err != nil || !replay.Replayed || replay.Character.MP != 94 || replay.Character.EXP != r.Character.EXP {
		t.Fatalf("replay result %+v %v", replay, err)
	}
	changed := q
	changed.OperationID += "-different"
	if _, err = s.ApplyPhysicalSkillAttack(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("same native tick new operation accepted: %v", err)
	}
	changed = q
	changed.Targets = append([]domain.PhysicalAttackTarget(nil), q.Targets...)
	changed.Targets[1].Damage++
	if _, err = s.ApplyPhysicalSkillAttack(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("same operation changed target accepted: %v", err)
	}
	a.Character = r.Character
	bad := q
	bad.Session = a
	bad.ClientTick++
	bad.OperationID += "-bad-last"
	bad.Targets = append([]domain.PhysicalAttackTarget(nil), q.Targets...)
	for i := range bad.Targets {
		bad.Targets[i].SpawnOperationID += "-bad"
	}
	bad.Targets[1].Experience++
	if _, err = s.ApplyPhysicalSkillAttack(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid final target accepted: %v", err)
	}
	loaded, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || loaded.MP != 94 || loaded.EXP != r.Character.EXP || loaded.Revision != r.Character.Revision {
		t.Fatalf("partial attack mutated character: %+v %v", loaded, err)
	}
	bad.Targets[1].Experience = mob.EXP
	bad.Targets[1].ObjectID = bad.Targets[0].ObjectID
	if _, err = s.ApplyPhysicalSkillAttack(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("duplicate target accepted: %v", err)
	}
	// Fail the final receipt INSERT, after MP, both kill rewards/quest counters
	// and loot jobs have been written. PostgreSQL must roll all of them back.
	if _, err = s.db.ExecContext(ctx, `CREATE FUNCTION maple_go_test_physical_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected physical receipt failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS maple_go_test_physical_failure ON asset_physical_attacks`)
		_, _ = s.db.ExecContext(ctx, `DROP FUNCTION IF EXISTS maple_go_test_physical_failure()`)
	})
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER maple_go_test_physical_failure BEFORE INSERT ON asset_physical_attacks FOR EACH ROW EXECUTE FUNCTION maple_go_test_physical_failure()`); err != nil {
		t.Fatal(err)
	}
	failing := q
	failing.Session = a
	failing.ClientTick = 200
	failing.OperationID += "-commit-fail"
	failing.Targets = append([]domain.PhysicalAttackTarget(nil), q.Targets...)
	for i := range failing.Targets {
		failing.Targets[i].SpawnOperationID += "-commit-fail"
	}
	if _, err = s.ApplyPhysicalSkillAttack(ctx, failing); err == nil {
		t.Fatal("receipt storage failure ignored")
	}
	loaded, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || loaded.MP != 94 || loaded.EXP != r.Character.EXP || loaded.Revision != r.Character.Revision {
		t.Fatalf("failed commit leaked vitals/EXP: %+v %v", loaded, err)
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs WHERE owner_id=$1`, a.CharacterID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed transaction leaked loot jobs: %d %v", count, err)
	}
}

func TestFirstWarriorCombatLevelPersistsSPAndGrowth(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	needed := content.ExperienceForLevel(10)
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=10,str=50,dex=4,sp=1,exp=$1 WHERE id=$2`, needed-1, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	before := a.Character
	r, err := s.AwardCombat(ctx, a, fmt.Sprintf("warrior-normal-level-operation-%d", a.CharacterID), 1)
	if err != nil || r.Level != 11 || r.EXP != 0 || r.SP != 4 || r.AP != before.AP+5 || r.STR != before.STR || r.MaxHP < before.MaxHP+48 || r.MaxHP > before.MaxHP+52 || r.MaxMP < before.MaxMP+4 || r.MaxMP > before.MaxMP+6 {
		t.Fatalf("warrior level %+v %v", r, err)
	}
	loaded, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || loaded.SP != 4 || loaded.Level != 11 || loaded.HP != loaded.MaxHP || loaded.MP != loaded.MaxMP {
		t.Fatalf("warrior growth not durable %+v %v", loaded, err)
	}
}
