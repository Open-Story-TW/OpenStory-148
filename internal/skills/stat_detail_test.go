package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestStatDetailFromSources(t *testing.T) {
	got := statDetailSources{
		Bonus: Bonus{EXP: 10, MesoRate: 77, CriticalDamage: 15, DamagePercent: 20, BossDamagePercent: 30, IgnoreDefense: 25,
			ASR: 11, TER: 12, AvoidRate: 13, HPOnHit: 99},
		HPSteal: 5, DamageReduce: 40,
		ExpRate: 3, MesoRate: 2, DropRate: 4, DamageCap: 99999999999,
	}.detail()
	want := protocol.StatDetail{
		ExpRate: 330, DropRate: 400, MesoRate: 200, PointRate: 100, // EXP 3× then +10%; Bonus.MesoRate is not applied.
		DamageCap:  99999999999,
		CritDamage: 65, TotalDamage: 20, BossDamage: 30, IgnoreDefense: 25,
		HPSteal: 5, StatusResist: 11, ElementResist: 12, DamageReduce: 40, AvoidChance: 13,
	}
	if got != want {
		t.Fatalf("detail\n got %+v\nwant %+v", got, want)
	}
}

func TestStatDetailDefaultsAndPotentialDamage(t *testing.T) {
	plain := statDetailSources{ExpRate: 1, MesoRate: 1, DropRate: 1, DamageCap: 999999}.detail()
	if want := (protocol.StatDetail{ExpRate: 100, DropRate: 100, MesoRate: 100, PointRate: 100, DamageCap: 999999, CritDamage: 50}); plain != want {
		t.Fatalf("plain detail %+v, want %+v", plain, want)
	}
	// 120% × 1.10 = 132% on ordinary monsters; (120+30)% × 1.10 × 1.20 = 198% on bosses.
	d := statDetailSources{Bonus: Bonus{DamagePercent: 20, BossDamagePercent: 30, IgnoreDefense: 250, EXP: -5},
		DamagePercents: []int{10}, BossDamagePercents: []int{10, 20}, ExpRate: 2, MesoRate: 1, DropRate: 1}.detail()
	if d.TotalDamage != 32 || d.BossDamage != 66 || d.IgnoreDefense != 100 || d.ExpRate != 200 {
		t.Fatalf("detail %+v", d)
	}
}

func TestStatDetailOfCharacter(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	domain.SetRates(5, 3, 2)
	protocol.SetLongDamageCap(0)
	t.Cleanup(func() { domain.SetRates(1, 1, 1) })
	now := time.Unix(1700000000, 0)
	live, gone := now.Add(time.Minute), now.Add(-time.Minute)
	c := domain.Character{ID: 1, Level: 100, Job: 100, HP: 1000, MaxHP: 1000, SkillBuffs: []domain.SkillBuff{
		{ID: 3121002, Stat: "SHARP_EYES", Value: 10<<8 | 15, ExpiresAt: live},
		{ID: 21100005, Stat: "COMBO_DRAIN", Value: 4, ExpiresAt: live},
		{ID: 1, Stat: "DAMAGE_TAKEN_BUFF", Value: 20, ExpiresAt: live},
		{ID: 2, Stat: "ABNORMAL_STATUS_R", Value: 30, ExpiresAt: live},
		{ID: 2, Stat: "ELEMENTAL_STATUS_R", Value: 35, ExpiresAt: live},
		{ID: 3, Stat: "ONYX_SHROUD", Value: 25, ExpiresAt: live},
		{ID: 35121006, Stat: "SATELLITESAFE_ABSORB", Value: 50, ExpiresAt: live},
		{ID: 35121006, Stat: "PUPPET", Value: 1, ExpiresAt: live},
		{ID: 4, Stat: "WATER_SHIELD", Value: 90, ExpiresAt: gone},
	}}
	got := StatDetail(c, now)
	want := protocol.StatDetail{ExpRate: 500, DropRate: 200, MesoRate: 300, PointRate: 100, DamageCap: 999999,
		CritDamage: 65, HPSteal: 4, StatusResist: 30, ElementResist: 35, DamageReduce: 20, AvoidChance: 25}
	if got != want {
		t.Fatalf("detail\n got %+v\nwant %+v", got, want)
	}
	protocol.SetLongDamageCap(5000000000)
	t.Cleanup(func() { protocol.SetLongDamageCap(0) })
	if got = StatDetail(c, now); got.DamageCap != 5000000000 {
		t.Fatalf("long damage cap %d", got.DamageCap)
	}
}
