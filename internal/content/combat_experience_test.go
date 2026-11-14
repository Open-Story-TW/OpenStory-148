package content

import (
	"math"
	"testing"

	"maple-go-server/internal/domain"
)

func expMember(id int64, level int) domain.CharacterSession {
	return domain.CharacterSession{CharacterID: id, SessionID: "exp-session", Generation: 1, ChannelID: 1, Lease: domain.WorldLease{WorldID: 0}, Character: domain.Character{ID: id, Level: level, HP: 10, MapID: 40000}}
}

func TestCombatExperienceRetainsMoreThan512HistoricalGenerations(t *testing.T) {
	rules := CombatExperienceRules{MaximumHP: 600, Experience: 600, MobLevel: 10, PartyBonusRate: 5}
	var damage []domain.CombatContribution
	for gen := int64(1); gen <= 600; gen++ {
		d := expDamage(1, 1)
		d.Generation = gen
		damage = append(damage, d)
	}
	current := expMember(1, 10)
	current.Generation = 600
	grants, err := CombatExperienceGrants(rules, 0, 1, 40000, damage, []domain.CharacterSession{current}, nil)
	if err != nil || len(grants) != 1 || grants[0].Experience != 1 {
		t.Fatalf("old generations were dropped, inherited or count-rejected: %+v %v", grants, err)
	}
	rules.MaximumHP = math.MaxInt64
	damage = []domain.CombatContribution{expDamage(1, math.MaxInt64-1), expDamage(2, math.MaxInt64-1)}
	if _, err = CombatExperienceGrants(rules, 0, 1, 40000, damage, nil, nil); err == nil {
		t.Fatal("overflowing damage sum passed the maximum-HP check")
	}
}
func expDamage(id, damage int64) domain.CombatContribution {
	return domain.CombatContribution{CharacterID: id, SessionID: "exp-session", Generation: 1, Damage: damage}
}
func TestPartyExperienceTwentyEightyAndFifteenPercent(t *testing.T) {
	rules := CombatExperienceRules{MaximumHP: 100, Experience: 100, MobLevel: 10, PartyBonusRate: 5}
	members := []domain.CharacterSession{expMember(1, 10), expMember(2, 10)}
	grants, err := CombatExperienceGrants(rules, 0, 1, 40000, []domain.CombatContribution{expDamage(1, 100)}, members, []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}})
	if err != nil || len(grants) != 2 || grants[0].BaseEXP != 60 || grants[0].PartyEXP != 9 || grants[0].Experience != 69 || grants[1].BaseEXP != 40 || grants[1].PartyEXP != 6 || grants[1].Experience != 46 {
		t.Fatalf("20/80+15%%: %+v %v", grants, err)
	}
}
func TestPartyExperienceStableContributorOrderRetainsLastMetadata(t *testing.T) {
	rules := CombatExperienceRules{MaximumHP: 100, Experience: 100, MobLevel: 30, PartyBonusRate: 5}
	members := []domain.CharacterSession{expMember(1, 10), expMember(2, 15), expMember(3, 20)}
	parties := []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{3, 1, 2}}}
	for _, damage := range [][]domain.CombatContribution{{expDamage(1, 50), expDamage(3, 50)}, {expDamage(3, 50), expDamage(1, 50)}} {
		grants, err := CombatExperienceGrants(rules, 0, 1, 40000, damage, members, parties)
		if err != nil || len(grants) != 3 {
			t.Fatal(grants, err)
		}
		b := grants[1]
		if b.CharacterID != 2 || b.BaseEXP != 30 || b.PartySize != 2 || b.PartyEXP != 4 || b.Experience != 34 {
			t.Fatalf("last contributor metadata not deterministic: %+v", grants)
		}
	}
}
func TestCombatExperienceSoloSharesAndDisconnectedGeneration(t *testing.T) {
	rules := CombatExperienceRules{MaximumHP: 100, Experience: 7, MobLevel: 1, PartyBonusRate: 5}
	members := []domain.CharacterSession{expMember(1, 10), expMember(2, 10)}
	grants, err := CombatExperienceGrants(rules, 0, 1, 40000, []domain.CombatContribution{expDamage(1, 25), expDamage(2, 75)}, members, nil)
	if err != nil || len(grants) != 2 || grants[0].Experience != 2 || grants[1].Experience != 6 {
		t.Fatalf("solo ceil shares: %+v %v", grants, err)
	}
	rules.Experience = 100
	old := expDamage(1, 50)
	old.Generation = 0 + 2
	grants, err = CombatExperienceGrants(rules, 0, 1, 40000, []domain.CombatContribution{old, expDamage(1, 50)}, members[:1], nil)
	if err != nil || len(grants) != 1 || grants[0].Experience != 50 {
		t.Fatalf("reconnected generation inherited old contribution: %+v %v", grants, err)
	}
}
func TestCombatExperienceDeadSourceSharesButDeadReceiverDoesNot(t *testing.T) {
	rules := CombatExperienceRules{MaximumHP: 100, Experience: 100, MobLevel: 10, PartyBonusRate: 5}
	a, b := expMember(1, 10), expMember(2, 10)
	a.Character.HP = 0
	parties := []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}
	grants, err := CombatExperienceGrants(rules, 0, 1, 40000, []domain.CombatContribution{expDamage(1, 100)}, []domain.CharacterSession{a, b}, parties)
	if err != nil || len(grants) != 1 || grants[0].CharacterID != 2 || grants[0].Experience != 80 {
		t.Fatalf("dead source Java semantics: %+v %v", grants, err)
	}
	a.Character.MapID = 20000
	if grants, err = CombatExperienceGrants(rules, 0, 1, 40000, []domain.CombatContribution{expDamage(1, 100)}, []domain.CharacterSession{a, b}, parties); err != nil || len(grants) != 0 {
		t.Fatalf("out-of-map source gave EXP: %+v %v", grants, err)
	}
}
