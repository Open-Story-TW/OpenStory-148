package world

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func resourceWorldWZ(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for path, data := range map[string]string{
		"Item.wz/ItemOption.img.xml":           `<imgdir name="ItemOption.img"><imgdir name="040502"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="mpconReduce" value="33"/><int name="reduceCooltime" value="7"/></imgdir></imgdir></imgdir></imgdir>`,
		"Character.wz/Weapon/01302000.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`,
		"Skill.wz/100.img.xml":                 `<imgdir name="100.img"><imgdir name="skill"><imgdir name="1001004"><imgdir name="common"><int name="maxLevel" value="20"/><int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="damage" value="190"/></imgdir></imgdir></imgdir></imgdir>`,
	} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
}

func TestResourceGenericWorldReducedAdmissionAndExactPublication(t *testing.T) {
	for _, mp := range []int{4, 5} {
		t.Run(string(rune('0'+mp)), func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			resourceWorldWZ(t)
			combat.SetStatsProvider(skills.Stats)
			t.Cleanup(func() { combat.SetStatsProvider(nil) })
			c := &lives[0].session.Character
			c.MP = mp
			c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}}
			c.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 20, Options: [5]uint16{40502}}
			beforeHP := mob.hp
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				if q.Cost == nil || q.Cost.MP != 5 || q.Cost.CooldownMS != 3000 || q.Cost.Opcode != protocol.RecvMeleeAttack {
					t.Fatal("unadjusted world cost", q.Cost)
				}
				r := fakeSharedResult(q)
				for i := range r.Characters {
					if r.Characters[i].ID == q.Session.CharacterID {
						r.Characters[i].MP -= q.Cost.MP
						r.Characters[i].SkillCooldowns = append(r.Characters[i].SkillCooldowns, domain.SkillCooldown{ID: q.SkillID, ExpiresAt: time.Now().Add(3 * time.Second)})
					}
				}
				return r, nil
			}}
			handled, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, nativePotentialMeleeBody(1001004, mob.objectID, 1))
			if err != nil || !handled {
				t.Fatal(handled, err)
			}
			if mp == 4 {
				if calls != 0 || mob.hp != beforeHP || c.MP != 4 {
					t.Fatal("insufficient reduced cost mutated", calls, mob.hp, c.MP)
				}
			} else if calls != 1 || c.MP != 0 {
				t.Fatal("reduced admission/publication", calls, c.MP)
			}
		})
	}
}

const resourcePaidSummonID = 9990413

func init() {
	skills.Register(skills.Handler{ID: resourcePaidSummonID, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true,
		Effect: combat.Effect{SkillID: resourcePaidSummonID, MPCon: 90, HPCon: 20, Cooltime: time.Minute, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{Duration: time.Minute}}})
}

func TestResourceOwnedPaidSummonWorksWithZeroMPAndLowHP(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	c := &lives[0].session.Character
	c.HP = 1
	c.MP = 0
	c.Skills = append(c.Skills, domain.SkillState{ID: resourcePaidSummonID, Level: 2})
	oid, err := s.SpawnSummon(1, lives[0], resourcePaidSummonID, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mob.hp = 1000
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 42, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 1, Damage: 1}}}}}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if mob.hp != 999 || calls != 0 || c.MP != 0 || c.HP != 1 || len(c.SkillCooldowns) != 0 {
		t.Fatal("summon repeated cast cost", mob.hp, calls, c.MP, c.HP)
	}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[1], sinks[1], in); err != nil {
		t.Fatal(err)
	}
	if mob.hp != 999 {
		t.Fatal("foreign owner got free summon hit")
	}
}

func TestResourceUnknownSettlementResolvesOriginalCostWithoutResend(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	resourceWorldWZ(t)
	combat.SetStatsProvider(skills.Stats)
	t.Cleanup(func() { combat.SetStatsProvider(nil) })
	c := &lives[0].session.Character
	c.MP = 5
	c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}}
	c.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 20, Options: [5]uint16{40502}}
	var committed domain.CombatAttackResult
	applies, lookups := 0, 0
	repo := &sharedCombatRepo{}
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		applies++
		committed = fakeSharedResult(q)
		for i := range committed.Characters {
			if committed.Characters[i].ID == q.Session.CharacterID {
				committed.Characters[i].MP -= q.Cost.MP
			}
		}
		return domain.CombatAttackResult{}, errors.New("simulated lost commit reply")
	}
	repo.outcome = func(_, hash string) (*domain.CombatAttackResult, error) {
		lookups++
		if lookups == 1 {
			return nil, errors.New("lookup unavailable")
		}
		if hash != committed.ManifestHash {
			t.Fatal("original cost witness changed")
		}
		return &committed, nil
	}
	s.repo = repo
	if _, err := s.genericAttackRoute(context.Background(), 1, lives[0], sinks[0], protocol.RecvMeleeAttack, nativePotentialMeleeBody(1001004, mob.objectID, 1)); err != nil {
		t.Fatal(err)
	}
	if applies != 1 || c.MP != 5 || !mob.claiming || len(s.gameplay.pendingCombat) != 1 {
		t.Fatal("guessed unknown cost", applies, c.MP, mob.claiming)
	}
	s.resolveCombatSettlements(context.Background(), repo)
	if applies != 1 || c.MP != 0 || mob.claiming || len(s.gameplay.pendingCombat) != 0 {
		t.Fatal("cost was resent or failed to publish receipt", applies, c.MP, mob.claiming)
	}
	s.resolveCombatSettlements(context.Background(), repo)
	if applies != 1 || c.MP != 0 {
		t.Fatal("resolved receipt charged again", applies, c.MP)
	}
}
