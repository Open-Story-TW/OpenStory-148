package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"maple-go-server/ent/migrate"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// Private schemas exercise the real migration runner without modifying shared
// public fixtures. Cleanup only drops the generated and verified test schema.
func useCombatSchema(t *testing.T) {
	t.Helper()
	if os.Getenv("MAPLEGO_TEST_INTEGRATION") != "1" {
		t.Skip("isolated PostgreSQL required")
	}
	original := os.Getenv("MAPLEGO_DATABASE_URL")
	admin, err := sql.Open("pgx", original)
	if err != nil {
		t.Fatal("open test database")
	}
	suffix, err := randomToken(6)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_combat_" + suffix
	if !regexp.MustCompile(`^test_combat_[0-9a-f]{12}$`).MatchString(schema) {
		t.Fatal("invalid generated test schema")
	}
	if _, err = admin.ExecContext(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if !regexp.MustCompile(`^test_combat_[0-9a-f]{12}$`).MatchString(schema) {
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
	var current string
	err = probe.QueryRow(`SELECT current_schema()`).Scan(&current)
	if err != nil || current != schema {
		_ = probe.Close()
		t.Fatal("search_path did not select the generated test schema")
	}
	if err = migrate.Apply(context.Background(), probe); err != nil {
		_ = probe.Close()
		t.Fatal(err)
	}
	var installed bool
	if err = probe.QueryRow(`SELECT to_regclass('asset_combat_attacks') IS NOT NULL`).Scan(&installed); err != nil {
		_ = probe.Close()
		t.Fatal(err)
	}
	if !installed {
		_ = probe.Close()
		t.Fatal("migration runner omitted immutable0038")
	}
	_ = probe.Close()
	t.Setenv("MAPLEGO_DATABASE_URL", uri.String())
	t.Setenv("MAPLEGO_WZ_ROOT", "")
}

func settlementFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	useCombatSchema(t)
	ctx, s, actors := inventoryFixture(t)
	var err error
	for i := range actors {
		if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET level=10,str=1000,hp=50,base_max_hp=100,max_hp=100,mp=100,base_max_mp=100,max_mp=100,map_id=40000,x=0,y=0,sp=3 WHERE id=$1`, actors[i].CharacterID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(ctx, `INSERT INTO game_quests(character_id,quest_id,state,mob_kills) VALUES($1,1035,1,'{}')`, actors[i].CharacterID); err != nil {
			t.Fatal(err)
		}
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return ctx, s, actors
}

func settlementParty(t *testing.T, ctx context.Context, s *Store, actors []domain.CharacterSession) {
	t.Helper()
	created, err := s.MutateParty(ctx, domain.PartyRequest{Session: actors[0], Action: domain.PartyCreate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: actors[0], Action: domain.PartyInvite, TargetID: actors[1].CharacterID, Name: actors[1].Character.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: actors[1], Action: domain.PartyAccept, PartyID: created.After.ID}); err != nil {
		t.Fatal(err)
	}
}

func settlementRequest(t *testing.T, ctx context.Context, s *Store, actors []domain.CharacterSession, family domain.CombatFamily, targets int) domain.CombatAttackRequest {
	t.Helper()
	q := domain.CombatAttackRequest{Session: actors[0], OperationID: fmt.Sprintf("settle-%d-%d-%d", actors[0].CharacterID, family, time.Now().UnixNano()), ClientTick: 7, Family: family, Members: append([]domain.CharacterSession(nil), actors...)}
	if family == domain.CombatPhysicalSkill {
		q.SkillID = 1001005
	} else if family == domain.CombatThreeSnails {
		q.SkillID = 1000
		q.LinesPerTarget = 1
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mob, err := catalog.Mob(9300018)
	if err != nil {
		t.Fatal(err)
	}
	currentDamage := int(mob.HP)
	if family == domain.CombatThreeSnails {
		currentDamage = min(currentDamage, 10)
	}
	for i := 0; i < targets; i++ {
		q.Targets = append(q.Targets, domain.CombatTarget{ObjectID: int32(1000001 + i), MobID: 9300018, SpawnOperationID: fmt.Sprintf("mob-%s-%d", q.OperationID, i), BeforeHP: int64(currentDamage), Damage: currentDamage, Defeated: true, Contributions: []domain.CombatContribution{{CharacterID: actors[0].CharacterID, SessionID: actors[0].SessionID, Generation: actors[0].Generation, Damage: mob.HP}}})
		if family == domain.CombatThreeSnails {
			q.Targets[i].Lines = []domain.CombatDamageLine{{Raw: uint32(currentDamage), Applied: currentDamage}}
		}
	}
	q.Parties, err = s.CombatParties(ctx, actors[0].Lease, combatContributorIDs(q.Targets))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestStagedCombatSettlementAllFamiliesShareAtomically(t *testing.T) {
	for _, family := range []domain.CombatFamily{domain.CombatNormal, domain.CombatThreeSnails, domain.CombatPhysicalSkill} {
		t.Run(fmt.Sprint(family), func(t *testing.T) {
			ctx, s, actors := settlementFixture(t)
			settlementParty(t, ctx, s, actors)
			count, cost := 1, 0
			if family == domain.CombatPhysicalSkill {
				count, cost = 2, 6
				if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100 WHERE id=$1`, actors[0].CharacterID); err != nil {
					t.Fatal(err)
				}
				var err error
				actors[0].Character, err = s.GetCharacter(ctx, actors[0].CharacterID)
				if err != nil {
					t.Fatal(err)
				}
				v, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1001004, OperationID: "staged-power-allocate"})
				if err != nil {
					t.Fatal(err)
				}
				actors[0].Character = v.Character
				v, err = s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1001005, OperationID: "staged-slash-allocate"})
				if err != nil {
					t.Fatal(err)
				}
				actors[0].Character = v.Character
			} else if family == domain.CombatThreeSnails {
				cost = 3
				v, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1000, OperationID: "staged-snails-allocate"})
				if err != nil {
					t.Fatal(err)
				}
				actors[0].Character = v.Character
			}
			// These are authenticated, same-revision, unsaved world-progress snapshots,
			// deliberately different from the older checkpoint in PostgreSQL.
			actors[0].Character.HP, actors[0].Character.MP, actors[0].Character.EXP, actors[0].Character.AP = 37, 90, 7, 8
			actors[1].Character.HP, actors[1].Character.MP, actors[1].Character.EXP, actors[1].Character.AP = 11, 13, 9, 4
			q := settlementRequest(t, ctx, s, actors, family, count)
			result, err := s.ApplyCombatAttack(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Characters) != 2 || len(result.Spawns) != count {
				t.Fatalf("missing recipients/spawns: %+v", result)
			}
			for i, a := range actors {
				c, err := s.GetCharacter(ctx, a.CharacterID)
				if err != nil {
					t.Fatal(err)
				}
				wantEXP := int64(7 + 2*count)
				wantMP := 90 - cost
				wantHP, wantAP := 37, 8
				if i == 1 {
					wantEXP = int64(9 + count)
					wantMP, wantHP, wantAP = 13, 11, 4
				}
				if c.EXP != wantEXP || c.MP != wantMP || c.HP != wantHP || c.AP != wantAP || c.SP != a.Character.SP || findQuest(c.Quests, 1035).MobKills[9300018] != 1 {
					t.Fatalf("partial or clobbered shared reward: %+v", c)
				}
			}
			replay, err := s.ApplyCombatAttack(ctx, q)
			if err != nil || !replay.Replayed || len(replay.Spawns) != count {
				t.Fatalf("shared replay: %+v %v", replay, err)
			}
			hash, _ := domain.CombatManifestHash(q)
			outcome, err := s.CombatAttackOutcome(ctx, q.Session.Lease, q.OperationID, hash)
			if err != nil || outcome == nil || !outcome.Replayed {
				t.Fatalf("commit outcome: %+v %v", outcome, err)
			}
			var loot, grants int
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs`).Scan(&loot); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_grants`).Scan(&grants); err != nil || loot != count || grants != 2*count {
				t.Fatalf("loot/grants=%d/%d %v", loot, grants, err)
			}
			changed := q
			changed.Members = changed.Members[:1]
			if _, err = s.ApplyCombatAttack(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("same command changed receivers: %v", err)
			}
		})
	}
}

func TestStagedCombatSettlementLateFailureRollsBackEveryRecipient(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	q := settlementRequest(t, ctx, s, actors, domain.CombatNormal, 1)
	if _, err := s.db.ExecContext(ctx, `CREATE FUNCTION staged_combat_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'late attack receipt failure'; END $$; CREATE TRIGGER staged_combat_fail BEFORE INSERT ON asset_combat_attacks FOR EACH ROW EXECUTE FUNCTION staged_combat_fail()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyCombatAttack(ctx, q); err == nil {
		t.Fatal("late receipt failure accepted")
	}
	for _, a := range actors {
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil || c.EXP != a.Character.EXP || c.Revision != a.Character.Revision || findQuest(c.Quests, 1035).MobKills[9300018] != 0 {
			t.Fatalf("partial settlement: %+v %v", c, err)
		}
	}
	for _, table := range []string{"asset_combat_rewards", "asset_combat_settlements", "asset_combat_grants", "asset_combat_loot_jobs", "game_quest_kill_receipts"} {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rolled-back %s count=%d %v", table, n, err)
		}
	}
}

func TestStagedCombatSettlementRejectsChangedPartyAndStaleRecipient(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	q := settlementRequest(t, ctx, s, actors, domain.CombatNormal, 1)
	if _, err := s.MutateParty(ctx, domain.PartyRequest{Session: actors[1], Action: domain.PartyLeave}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyCombatAttack(ctx, q); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("changed party pin accepted: %v", err)
	}
	q.Parties, _ = s.CombatParties(ctx, actors[0].Lease, combatContributorIDs(q.Targets))
	q.Members[1].Generation++
	if _, err := s.ApplyCombatAttack(ctx, q); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale receiver accepted: %v", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_attacks`).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid context wrote an attack")
	}
}

func TestStagedCombatConcurrentCommandsAndSingleLoot(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	q := settlementRequest(t, ctx, s, actors, domain.CombatNormal, 1)
	var wg sync.WaitGroup
	fresh := make(chan bool, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.ApplyCombatAttack(ctx, q)
			if e != nil {
				t.Errorf("repeated shared command: %v", e)
				return
			}
			fresh <- !r.Replayed
		}()
	}
	wg.Wait()
	close(fresh)
	commits := 0
	for f := range fresh {
		if f {
			commits++
		}
	}
	if commits != 1 {
		t.Fatalf("shared receipt commits=%d", commits)
	}
	for i := range actors {
		var err error
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
	}
	other := settlementRequest(t, ctx, s, []domain.CharacterSession{actors[1], actors[0]}, domain.CombatNormal, 1)
	other.ClientTick = 8
	if _, err := s.ApplyCombatAttack(ctx, other); err != nil {
		t.Fatalf("opposite actor settlement: %v", err)
	}
	for _, a := range actors {
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil || c.EXP != 3 {
			t.Fatalf("opposite actor sharing: %+v %v", c, err)
		}
	}
	var loot int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs`).Scan(&loot); err != nil || loot != 2 {
		t.Fatalf("one loot per spawn count=%d %v", loot, err)
	}
}

func TestStagedCombatLegacySoloAPIsRejectParty(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	if _, err := s.AwardCombatKillAt(ctx, actors[0], "compat-party-must-not-award", 3, 9300018, 0, 0); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("legacy reward silently omitted party: %v", err)
	}
	if _, err := s.ApplySkillAttack(ctx, domain.SkillAttackRequest{Session: actors[0], OperationID: "compat-snails-must-not-award", SkillID: 1000}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("legacy snails silently omitted party: %v", err)
	}
	if _, err := s.ApplyPhysicalSkillAttack(ctx, domain.PhysicalAttackRequest{Session: actors[0], OperationID: "compat-physical-must-not-award", SkillID: 1001005}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("legacy physical silently omitted party: %v", err)
	}
}

func TestStagedCombatQuestDropPartyRegression(t *testing.T) {
	useCombatSchema(t)
	TestQuestDropPartyOwnerRuleAndTwoActorFFA(t)
}

func TestStagedCombatBothRecipientsLevelAndRetainDistinctGrowth(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	// One beginner and one first warrior earn a level in the same transaction.
	// The unsaved EXP is deliberately supplied by the locked live snapshots.
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100 WHERE id=$1`, actors[1].CharacterID); err != nil {
		t.Fatal(err)
	}
	for i := range actors {
		var err error
		actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		actors[i].Character.EXP = content.ExperienceForLevel(10) - int64(2-i)
	}
	settlementParty(t, ctx, s, actors)
	q := settlementRequest(t, ctx, s, actors, domain.CombatNormal, 1)
	if _, err := s.ApplyCombatAttack(ctx, q); err != nil {
		t.Fatal(err)
	}
	for i, a := range actors {
		c, err := s.GetCharacter(ctx, a.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		wantSP, minHPGain, minMPGain := a.Character.SP, 12, 10
		if i == 1 {
			wantSP, minHPGain, minMPGain = wantSP+3, 48, 4
		}
		if c.Level != 11 || c.EXP != 0 || c.AP != a.Character.AP+5 || c.SP != wantSP || c.STR != a.Character.STR || c.HP != c.MaxHP || c.MP != c.MaxMP || c.BaseMaxHP < a.Character.BaseMaxHP+minHPGain || c.BaseMaxMP < a.Character.BaseMaxMP+minMPGain {
			t.Fatalf("recipient%d growth was omitted or clobbered: %+v", i, c)
		}
	}
}

func TestStagedCombatOppositeActorsLockWithoutDeadlock(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	commands := []domain.CombatAttackRequest{
		settlementRequest(t, ctx, s, actors, domain.CombatNormal, 1),
		settlementRequest(t, ctx, s, []domain.CharacterSession{actors[1], actors[0]}, domain.CombatNormal, 1),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, q := range commands {
		go func(q domain.CombatAttackRequest) {
			<-start
			call, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, err := s.ApplyCombatAttack(call, q)
			results <- err
		}(q)
	}
	close(start)
	commits, stale := 0, 0
	for range commands {
		err := <-results
		if err == nil {
			commits++
		} else if errors.Is(err, domain.ErrStaleOwner) {
			stale++
		} else {
			t.Fatalf("opposite actor lock failed: %v", err)
		}
	}
	if commits != 1 || stale != 1 {
		t.Fatalf("expected one commit and one stale snapshot: %d/%d", commits, stale)
	}
}

func TestStagedCombatMagicLineForgeryRollsBackCostAndRewards(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	settlementParty(t, ctx, s, actors)
	allocated, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1000, OperationID: "magic-lines-allocate"})
	if err != nil {
		t.Fatal(err)
	}
	actors[0].Character = allocated.Character
	base := settlementRequest(t, ctx, s, actors, domain.CombatThreeSnails, 1)
	for _, kind := range []string{"count", "raw", "applied", "critical", "absent"} {
		q := base
		q.Targets = append([]domain.CombatTarget(nil), base.Targets...)
		q.Targets[0].Lines = append([]domain.CombatDamageLine(nil), base.Targets[0].Lines...)
		switch kind {
		case "count":
			q.LinesPerTarget = 2
		case "raw":
			q.Targets[0].Lines[0].Raw = 11
		case "applied":
			q.Targets[0].Lines[0].Applied = 9
		case "critical":
			q.Targets[0].Lines[0].Raw = 0x8000000a
		case "absent":
			q.Targets[0].Lines = nil
		}
		if _, err = s.ApplyCombatAttack(ctx, q); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%s line forgery accepted: %v", kind, err)
		}
	}
	for _, actor := range actors {
		c, err := s.GetCharacter(ctx, actor.CharacterID)
		if err != nil || c.MP != actor.Character.MP || c.EXP != actor.Character.EXP || c.Revision != actor.Character.Revision {
			t.Fatalf("magic validation leaked cost/reward: %+v %v", c, err)
		}
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_attacks`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid lines wrote a receipt")
	}
}

func TestStagedCombatNativeMagicWireReceiptAndMissCost(t *testing.T) {
	for _, hit := range []bool{false, true} {
		t.Run(fmt.Sprint(hit), func(t *testing.T) {
			ctx, s, actors := settlementFixture(t)
			allocated, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1000, OperationID: "native-wire-learn1000"})
			if err != nil {
				t.Fatal(err)
			}
			actors[0].Character = allocated.Character
			count := 0
			raw := "0101e803000000fd0bc55a002c0051955cc90106673b58020000000044019b0000"
			if hit {
				count = 1
				raw = "0211e803000000fd0bc55a002c8051955cc901061a2968020000000053420f00078001011c028b011e028b01c7010a000000e1839dd701ac81e241028b0100"
			}
			q := settlementRequest(t, ctx, s, actors, domain.CombatThreeSnails, count)
			q.Wire, err = hex.DecodeString(raw)
			if err != nil {
				t.Fatal(err)
			}
			q.ClientTick = 39336807
			if hit {
				q.ClientTick = 40380698
				q.Targets[0].ObjectID = 1000019
				bad := q
				bad.Wire = append([]byte(nil), q.Wire...)
				bad.Wire[46] = 9
				if _, err = s.ApplyCombatAttack(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
					t.Fatal("raw wire/line mismatch accepted")
				}
			}
			if _, err = s.ApplyCombatAttack(ctx, q); err != nil {
				t.Fatal(err)
			}
			c, err := s.GetCharacter(ctx, actors[0].CharacterID)
			if err != nil || c.MP != actors[0].Character.MP-3 {
				t.Fatal("native miss/hit cost not committed exactly once", err)
			}
			var manifestRaw []byte
			if err = s.db.QueryRowContext(ctx, `SELECT manifest FROM asset_combat_attacks WHERE operation_id=$1`, q.OperationID).Scan(&manifestRaw); err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Wire           []byte
				LinesPerTarget int
				Targets        []domain.CombatTarget
			}
			if err = json.Unmarshal(manifestRaw, &manifest); err != nil || !bytes.Equal(manifest.Wire, q.Wire) || manifest.LinesPerTarget != 1 || len(manifest.Targets) != count {
				t.Fatalf("native wire/line evidence lost: %s %v", manifestRaw, err)
			}
			if hit && (manifest.Targets[0].Lines[0].Raw != 10 || manifest.Targets[0].Lines[0].Applied != 10) {
				t.Fatal("per-line damage collapsed to sum")
			}
			var loot int
			if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_combat_loot_jobs`).Scan(&loot); err != nil || loot != count {
				t.Fatal("miss created loot or hit omitted it")
			}
		})
	}
}
