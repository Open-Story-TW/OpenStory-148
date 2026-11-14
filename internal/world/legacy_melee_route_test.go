package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Exact original 2026-11-06 native Power Strike: bodySHA460763B6...B44A25F.
// Neither the unmarked noncritical raw448 nor the collision-displaced feet32/215
// is normalized for the test. Scope is supplied by the ordinary world fixture.
const inheritedPowerStrikeWire = "4b0001112c460f0000573f0488000e8051955cc90206c346931b0000000062420f0007810a01aeffd700aeffd700c201c0010000eb016714a9a388312000d700"

func inheritedMeleeFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *gameplayMob, []byte) {
	t.Helper()
	s, lives, sinks, template := sharedWorldFixture(t)
	combat.SetStatsProvider(skills.Stats)
	t.Cleanup(func() { combat.SetStatsProvider(nil) })
	c := &lives[0].session.Character
	c.Job, c.Level, c.STR, c.DEX, c.INT, c.LUK, c.X, c.Y = 132, 150, 57, 500, 4, 4, 32, 215
	c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1432000, WATK: 32}}
	c.Skills = []domain.SkillState{{ID: 1001004, Level: 20}, {ID: 1001005, Level: 20}, {ID: 1300000, Level: 20}, {ID: 1310009, Level: 10}}
	s.online[1] = cloneCombatSession(lives[0].session)
	raw, err := hex.DecodeString(inheritedPowerStrikeWire)
	if err != nil {
		t.Fatal(err)
	}
	body := protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, raw[2:]) // archived v145 frame
	a, err := protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	m := *template
	m.objectID = a.Targets[0].ObjectID
	m.stats = content.Mob{ID: 9300306, Level: 129, HP: 550000000, PDRate: 50, Boss: true}
	m.hp = m.stats.HP
	m.life.X, m.life.Y = -82, 215
	m.contributions = nil
	s.gameplay.rooms[mapKey{1, 40000}].mobs[m.objectID] = &m
	return s, lives[0], sinks[0], &m, body
}

func inheritedMeleeResult(q domain.CombatAttackRequest) domain.CombatAttackResult {
	r := domain.CombatAttackResult{OperationID: q.OperationID}
	r.ManifestHash, _ = domain.CombatManifestHash(q)
	for _, m := range q.Members {
		c := cloneCombatSession(m).Character
		c.Revision++
		if c.ID == q.Session.CharacterID {
			c.MP -= q.Cost.MP
		}
		r.Characters = append(r.Characters, c)
	}
	return r
}

func TestInheritedMeleeLegacyOwnershipMatrix(t *testing.T) {
	for _, job := range []int{0, 100, 130, 110, 120, 131, 132, 200, 400, 999} {
		for _, skill := range []int{0, 1001004, 1001005} {
			want := job == 0 && skill == 0 // Warriors take the generic bound: passives and criticals count.
			if got := legacyAttack(protocol.RecvMeleeAttack, job, skill); got != want {
				t.Errorf("job%d skill%d legacy=%v want%v", job, skill, got, want)
			}
		}
		if !legacyAttack(protocol.RecvMagicAttack, job, 1000) || legacyAttack(protocol.RecvMagicAttack, job, 1001004) || legacyAttack(protocol.RecvShootAttack, job, 1001004) || legacyAttack(protocol.RecvMeleeAttack, job, 1311001) {
			t.Fatalf("unrelated family changed job%d", job)
		}
	}
}

func TestInheritedPowerStrikeNative448LiveDispatchAndUnknownReceipt(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			s, live, sink, mob, body := inheritedMeleeFixture(t)
			archived, _ := hex.DecodeString(inheritedPowerStrikeWire)
			hash := sha256.Sum256(archived[2:]) // the archived v145 frame; body is its v148 form
			if hex.EncodeToString(hash[:]) != "460763b67ce6efe64107ea400a1dd343793dd64ed27c55a3d9344eedcb44a25f" {
				t.Fatal("native source body changed")
			}
			if _, err := protocol.ReadMeleeAttack(protocol.NewReader(body)); err != nil {
				t.Fatal("valid legacy shape rejected", err)
			}
			var captured domain.CombatAttackRequest
			var committed domain.CombatAttackResult
			calls, lookups, ready := 0, 0, false
			repo := &sharedCombatRepo{}
			repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				captured = q
				// This is the same pure fresh cost-only policy used by genericCombatCost;
				// the fake repository does not skip class/level/opcode/resource validation.
				cost, _, err := skills.AttackResourceCost(q.Session.Character, q.SkillID, q.Cost.Level, q.Cost.Opcode, time.Now())
				if err != nil {
					return domain.CombatAttackResult{}, domain.ErrForbidden
				}
				if cost.MP != 11 || q.Cost.MP != cost.MP {
					t.Fatal("fresh cost mismatch", q.Cost, cost)
				}
				committed = inheritedMeleeResult(q)
				if unknown {
					return domain.CombatAttackResult{}, errors.New("commit acknowledgement lost")
				}
				return committed, nil
			}
			repo.outcome = func(op, manifest string) (*domain.CombatAttackResult, error) {
				lookups++
				if op != captured.OperationID || manifest != committed.ManifestHash {
					t.Fatal("outcome changed operation/hash")
				}
				if !ready {
					return nil, errors.New("receipt temporarily unavailable")
				}
				return &committed, nil
			}
			s.repo = repo
			check := func() {
				handled, err := s.handleGameplay(context.Background(), 1, live, sink, protocol.RecvMeleeAttack, protocol.NewReader(body))
				if err != nil || !handled {
					t.Fatal("dispatch failed", handled, err)
				}
				if unknown {
					if mob.hp != 550000000 || live.session.Character.MP != 100 {
						t.Fatal("unknown outcome published before confirmation")
					}
					ready = true
					s.resolveCombatSettlements(context.Background(), repo)
				}
			}
			row := captureNativeDamageEvidence(t, check)
			assertNativeDamageWire(t, row, body, 462636739)
			if calls != 1 || captured.Family != domain.CombatSkill || captured.SkillID != 1001004 || captured.Cost.Level != 20 || captured.Cost.Opcode != protocol.RecvMeleeAttack || len(captured.Targets) != 1 || captured.Targets[0].Damage != 448 {
				t.Fatal("wrong route or cost", calls, captured)
			}
			line := captured.Targets[0].Lines[0]
			if line.Raw != 448 || line.Applied != 448 || line.Criticality != domain.CombatCriticalUnknown {
				t.Fatal("reviewed Power Strike raw or criticality changed", line)
			}
			if mob.hp != 550000000-448 || live.session.Character.MP != 89 || live.session.Character.EXP != 0 {
				t.Fatal("actual HP/MP/progress mismatch", mob.hp, live.session.Character.MP, live.session.Character.EXP)
			}
			frozen, _ := domain.CombatManifestHash(captured)
			if frozen != committed.ManifestHash {
				t.Fatal("immutable manifest changed")
			}
			s.resolveCombatSettlements(context.Background(), repo)
			if calls != 1 || unknown && lookups == 0 || live.session.Character.MP != 89 || mob.hp != 550000000-448 {
				t.Fatal("outcome lookup executed or charged again", calls, lookups)
			}
			if !bytes.Equal(body, mustInheritedPowerStrikeBody(t)) {
				t.Fatal("native body mutated")
			}
		})
	}
}

func mustInheritedPowerStrikeBody(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(inheritedPowerStrikeWire)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, b[2:]) // archived v145 frame
}

func TestInheritedMeleeRouteKeepsEligibilityAndOwnershipGates(t *testing.T) {
	for _, job := range []int{110, 120, 131, 132, 200, 400, 999} {
		t.Run(fmt.Sprint(job), func(t *testing.T) {
			s, live, sink, mob, body := inheritedMeleeFixture(t)
			live.session.Character.Job = job
			s.online[1] = cloneCombatSession(live.session)
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				cost, _, err := skills.AttackResourceCost(q.Session.Character, q.SkillID, q.Cost.Level, q.Cost.Opcode, time.Now())
				if err != nil {
					return domain.CombatAttackResult{}, domain.ErrForbidden
				}
				if cost.MP != q.Cost.MP {
					t.Fatal("cost not fresh")
				}
				return inheritedMeleeResult(q), nil
			}}
			handled, err := s.genericAttackRoute(context.Background(), 1, live, sink, protocol.RecvMeleeAttack, body)
			if !handled || err != nil {
				t.Fatal("inherited route not generic", handled, err)
			}
			valid := job == 110 || job == 120 || job == 131 || job == 132
			wantHP, wantMP := int64(550000000), 100
			if valid {
				wantHP -= 448
				wantMP -= 11
			}
			if calls != 1 || mob.hp != wantHP || live.session.Character.MP != wantMP {
				t.Fatal("class/cost validation bypass", calls, mob.hp, live.session.Character.MP)
			}
		})
	}
	for _, fault := range []string{"dead", "noMP", "unlearned", "expired", "wrongOpcode", "staleSink", "staleGeneration"} {
		t.Run(fault, func(t *testing.T) {
			s, live, sink, mob, body := inheritedMeleeFixture(t)
			op := protocol.RecvMeleeAttack
			var targetSink packetSink = sink
			switch fault {
			case "dead":
				live.session.Character.HP = 0
			case "noMP":
				live.session.Character.MP = 10
			case "unlearned":
				live.session.Character.Skills = nil
			case "expired":
				live.session.Character.Skills[0].ExpiresAt = time.Now().Add(-time.Minute)
			case "wrongOpcode":
				op = protocol.RecvShootAttack
			case "staleSink":
				targetSink = &gameplayTestSink{}
			case "staleGeneration":
				live.session.Generation++
			}
			beforeMP := live.session.Character.MP
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return inheritedMeleeResult(q), nil
			}}
			handled, err := s.genericAttackRoute(context.Background(), 1, live, targetSink, op, body)
			if err != nil || !handled || calls != 0 || mob.hp != 550000000 || live.session.Character.MP != beforeMP {
				t.Fatal("guard bypass", handled, err, calls, mob.hp, live.session.Character.MP)
			}
		})
	}
}
