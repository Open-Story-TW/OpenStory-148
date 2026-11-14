package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

const powerStrikeCriticalR4Wire = "550001112c460f0000573f0488001480486874860206b606d41c0000000041420f0007810301afffd700afffd70090018d020000eb0167146cc0ce45ffffd700"

func TestReviewedPowerStrikeActualR4HPMPReplayAndUnknownReceipt(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "commit"
		if unknown {
			name = "unknown_commit"
		}
		t.Run(name, func(t *testing.T) {
			s, live, sink, mob, _ := inheritedMeleeFixture(t)
			wire, err := hex.DecodeString(powerStrikeCriticalR4Wire)
			if err != nil {
				t.Fatal(err)
			}
			body := wire[2:]
			original := append([]byte(nil), body...)
			if h := sha256.Sum256(body); hex.EncodeToString(h[:]) != "b28dc281499ec686a24bd52c4cd2b8a1e19f70393a5715cb97ef60e1e2759436" {
				t.Fatal("R4 original body changed")
			}
			body = protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, body) // archived v145 frame
			original = append([]byte(nil), body...)
			a, err := protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			room := s.gameplay.rooms[mapKey{1, 40000}]
			delete(room.mobs, mob.objectID)
			mob.objectID = a.Targets[0].ObjectID
			mob.life.X = -81
			room.mobs[mob.objectID] = mob
			c := &live.session.Character
			c.X, c.Y = int(a.X), int(a.Y)
			c.HP, c.MP, c.MaxHP, c.MaxMP = 3220, 1454, 3220, 1454
			c.Equipment = append(c.Equipment, domain.Equipment{ID: 86996, Slot: -1, ItemID: 1002019, WDEF: 8, Potential: domain.EquipmentPotential{Present: true, RawState: 18, Options: [5]uint16{20353, 10014}, Flags: 0xffff}})
			s.online[1] = cloneCombatSession(live.session)
			var captured domain.CombatAttackRequest
			var committed domain.CombatAttackResult
			calls, lookups, ready := 0, 0, false
			repo := &sharedCombatRepo{}
			repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				captured = q
				cost, _, err := skills.AttackResourceCost(q.Session.Character, q.SkillID, q.Cost.Level, q.Cost.Opcode, time.Now())
				if err != nil || cost.MP != 11 || q.Cost.MP != 11 {
					t.Fatal("fresh cost mismatch", cost, q.Cost, err)
				}
				committed = inheritedMeleeResult(q)
				if unknown {
					return domain.CombatAttackResult{}, errors.New("original commit reply lost")
				}
				return committed, nil
			}
			repo.outcome = func(op, hash string) (*domain.CombatAttackResult, error) {
				lookups++
				if op != captured.OperationID || hash != committed.ManifestHash {
					t.Fatal("resolver changed original operation/hash")
				}
				if !ready {
					return nil, errors.New("receipt temporarily unavailable")
				}
				return &committed, nil
			}
			s.repo = repo
			dispatch := func() {
				handled, err := s.handleGameplay(context.Background(), 1, live, sink, protocol.RecvMeleeAttack, protocol.NewReader(body))
				if err != nil || !handled {
					t.Fatal("dispatch failed", handled, err)
				}
			}
			row := captureNativeDamageEvidence(t, func() {
				dispatch()
				if unknown {
					if mob.hp != 550000000 || live.session.Character.MP != 1454 {
						t.Fatal("unknown fabricated HP/cost before resolve")
					}
					dispatch()
					if calls != 1 {
						t.Fatal("pending replay resubmitted Apply")
					}
					ready = true
					s.resolveCombatSettlements(context.Background(), repo)
				}
			})
			assertNativeDamageWire(t, row, body, 483657398)
			if calls != 1 || len(captured.Targets) != 1 || captured.Targets[0].Damage != 653 || captured.Cost.MP != 11 {
				t.Fatal("original653 clipped or cost changed", calls, captured)
			}
			line := captured.Targets[0].Lines[0]
			if line.Raw != 653 || line.Applied != 653 || line.Criticality != domain.CombatCriticalUnknown {
				t.Fatal("raw/unknown changed", line)
			}
			if mob.hp != 550000000-653 || live.session.Character.MP != 1443 {
				t.Fatal("actual HP/MP mismatch", mob.hp, live.session.Character.MP)
			}
			evidence := row["targets"].([]any)[0].(map[string]any)
			if evidence["hp_removed"] != float64(653) || evidence["line_criticality"].([]any)[0] != "unknown" {
				t.Fatal("actual evidence lost unknown/raw", evidence)
			}
			dispatch()
			s.resolveCombatSettlements(context.Background(), repo)
			if calls != 1 || unknown && lookups == 0 || mob.hp != 550000000-653 || live.session.Character.MP != 1443 || !bytes.Equal(body, original) {
				t.Fatal("replay/resolve repeated HP/cost or mutated raw", calls, lookups, mob.hp, live.session.Character.MP)
			}
		})
	}
}
