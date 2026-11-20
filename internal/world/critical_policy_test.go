package world

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestBossCriticalAuditKeepsOrdinaryHistoryIndependent(t *testing.T) {
	var audits targetCriticalAudits
	ordinary := audits.forLine(false, 20, 0)
	boss := audits.forLine(true, 20, 0)
	lateBoss := audits.forLine(true, 20, 3)
	if ordinary == boss || boss == lateBoss || ordinary == lateBoss || audits.forLine(true, 0, 3) != ordinary || audits.forLine(false, 20, 3) != ordinary {
		t.Fatal("only a boss with an active boss-rate bonus should use separate history")
	}

	const character = 1
	ordinary.Normal(character, 15)
	for i := 0; i < 250; i++ {
		if !lateBoss.Admit(character, 95) {
			t.Fatalf("native 100 percent line clipped at observation %d", i)
		}
		if i%5 >= 2 {
			boss.Normal(character, 35)
		} else if !boss.Admit(character, 35) {
			t.Fatalf("boss hit at the configured rate was rejected on line %d", i)
		}
	}
	if !ordinary.Admit(character, 15) {
		t.Fatal("boss history rejected a legal ordinary-target critical")
	}
}

func TestBossCriticalHistoryDoesNotAdvancePastMisses(t *testing.T) {
	var audits targetCriticalAudits
	clipped := 0
	for n := 0; n < 160; n++ {
		plan := combat.TargetPlan{BossCriticalStep: 20,
			Lines: []int{0, 0, 0, 150}, Normal: []int{100, 100, 100, 100},
			Critical: []bool{false, false, false, true}}
		audits.apply(&plan, true, 20, 35, 7, 5121007)
		if plan.Clipped != 0 {
			if plan.Lines[3] != 100 || plan.Critical[3] {
				t.Fatal("the limited line did not use its ordinary damage cap")
			}
			clipped++
		}
	}
	if clipped == 0 {
		t.Fatal("zero lines advanced the first connected hit to 100 percent")
	}
	for n := 0; n < 8; n++ {
		plan := combat.TargetPlan{BossCriticalStep: 20,
			Lines: []int{50, 50, 50, 150}, Normal: []int{100, 100, 100, 100},
			Critical: []bool{false, false, false, true}}
		audits.apply(&plan, true, 20, 35, 7, 5121007)
		if plan.Lines[3] != 150 || !plan.Critical[3] {
			t.Fatal("a fully connected fourth hit did not reach 100 percent")
		}
	}
}

func TestReviewedCriticalityEvidencePreservesUnknownWithoutSettingRawBit(t *testing.T) {
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "7")
	q := domain.CombatTarget{ObjectID: 1, MobID: 9300306, BeforeHP: 1000, Lines: []domain.CombatDamageLine{{Raw: 426, Applied: 426, Criticality: domain.CombatCriticalUnknown}}}
	ticket := &combatTicket{request: domain.CombatAttackRequest{Session: domain.CharacterSession{CharacterID: 7}}}
	ticket.recordAppliedDamageLocked(q, 574, true)
	if len(ticket.damageEvidence) != 1 {
		t.Fatal("missing actual evidence")
	}
	e := ticket.damageEvidence[0]
	if len(e.LineCriticality) != 1 || e.LineCriticality[0] != domain.CombatCriticalUnknown || e.ClaimedLines[0] != 426 || e.BoundedLines[0] != 426 {
		t.Fatal(e)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err = json.Unmarshal(b, &output); err != nil || output["line_criticality"].([]any)[0] != "unknown" {
		t.Fatal(string(b), err)
	}
}

func TestReviewedCrusherActualDamageAndUnknownReceiptKeepRawIdentity(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "commit"
		if unknown {
			name = "unknown_commit"
		}
		t.Run(name, func(t *testing.T) {
			s, lives, sinks, template := sharedWorldFixture(t)
			c := &lives[0].session.Character
			c.Job, c.Level, c.STR, c.DEX, c.INT, c.LUK, c.X, c.Y = 132, 150, 57, 500, 4, 4, -3, 215
			c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1432000, WATK: 32}}
			c.Skills = []domain.SkillState{{ID: 1311001, Level: 20}, {ID: 1300000, Level: 20}, {ID: 1310009, Level: 10}}
			s.online[1] = cloneCombatSession(lives[0].session)
			raw, _ := hex.DecodeString("4b0001331901140000f3ee5a92004a8045095a94020676b8441b0000000060420f000781060186ffd70086ffd7005802aa010000140100005a010000eb01671405e3f28f5f420f000781060166ffd70066ffd7009e023b0100005801000027010000eb016714ac9cf4495e420f000781060146ffd70046ffd700e4023d010000f10100001a010000eb016714a642b6f7fdffd700")
			raw = append(raw[:2:2], protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, raw[2:])...) // archived v145 frame
			a, err := protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(raw[2:]))
			if err != nil {
				t.Fatal(err)
			}
			room := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}]
			var mobs []*gameplayMob
			for i, target := range a.Targets {
				m := *template
				m.objectID = target.ObjectID
				m.stats = content.Mob{ID: 9300306, Level: 129, HP: 550000000, PDRate: 50, Boss: true}
				m.hp = m.stats.HP
				m.life.X = []int{-122, -154, -186}[i]
				m.life.Y = 215
				m.contributions = nil
				room.mobs[m.objectID] = &m
				mobs = append(mobs, &m)
			}
			var captured domain.CombatAttackRequest
			var committed domain.CombatAttackResult
			calls, ready := 0, false
			repo := &sharedCombatRepo{}
			repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				captured = q
				committed = fakeSharedResult(q)
				for i := range committed.Characters {
					if committed.Characters[i].ID == 1 {
						committed.Characters[i].MP -= 24
					}
				}
				if unknown {
					return domain.CombatAttackResult{}, errors.New("commit response lost")
				}
				return committed, nil
			}
			repo.outcome = func(op, hash string) (*domain.CombatAttackResult, error) {
				if op != captured.OperationID || hash != committed.ManifestHash {
					t.Error("resolver changed operation")
				}
				if !ready {
					return nil, errors.New("receipt not visible")
				}
				return &committed, nil
			}
			s.repo = repo
			if err = s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || captured.Cost.MP != 24 {
				t.Fatal("cost/call count", calls, captured.Cost)
			}
			for _, target := range captured.Targets {
				for _, line := range target.Lines {
					if line.Applied != int(line.Raw) || line.Criticality != domain.CombatCriticalUnknown || line.Raw&0x80000000 != 0 {
						t.Fatal("raw/unknown classification lost", line)
					}
				}
			}
			if unknown {
				for _, m := range mobs {
					if m.hp != 550000000 {
						t.Fatal("unknown commit fabricated damage")
					}
				}
				ready = true
				s.resolveCombatSettlements(context.Background(), repo)
			}
			var removed int64
			for _, m := range mobs {
				removed += 550000000 - m.hp
			}
			if removed != 3098 || lives[0].session.Character.MP != 76 {
				t.Fatal("actual HP/MP mismatch", removed, lives[0].session.Character.MP)
			}
			s.resolveCombatSettlements(context.Background(), repo)
			if calls != 1 {
				t.Fatal("unknown resolver executed a new attack")
			}
		})
	}
}
