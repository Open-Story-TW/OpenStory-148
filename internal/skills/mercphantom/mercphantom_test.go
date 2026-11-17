package mercphantom_test

import (
	"maple-go-server/internal/protocol"
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/mercphantom"
)

// pending are the thread MP skills that wait for shared core work
// (Open-Maple docs/server/skills/MP.md): Phantom's Impeccable Memory steal slots.
var pending = map[int]string{
	24001001: "steal slot I", 24101001: "steal slot II", 24111001: "steal slot III", 24121001: "steal slot IV",
}

const (
	dualBowgunID = 1522000
	caneID       = 1362000
)

var now = time.Unix(1000, 0)

func character(job, weaponID int, learned ...domain.SkillState) domain.Character {
	c := domain.Character{Job: job, Level: 200, Skills: learned}
	if weaponID != 0 {
		c.Equipment = []domain.Equipment{{ItemID: weaponID, Slot: -11}}
	}
	return c
}

func values(t *testing.T, id, level int) (content.Skill, map[string]int) {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return def, v
}

func cast(t *testing.T, id, level int, c domain.Character) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	return h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: c, Now: now})
}

func mustCast(t *testing.T, id, level int, c domain.Character) skills.CastPlan {
	t.Helper()
	p, err := cast(t, id, level, c)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func passive(t *testing.T, id, level int, c domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("skill %d has no passive", id)
	}
	def, v := values(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c, Now: now})
}

func spec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatalf("skill %d has no attack", id)
	}
	return h.Attack
}

// prepared runs the attack's Prepare on its WZ effect at level.
func prepared(t *testing.T, id, level int, c domain.Character) combat.Effect {
	t.Helper()
	def, _ := values(t, id, level)
	e, err := combat.FromSkill(def, level)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Character: c, Effect: e, Now: now}
	if s := spec(t, id); s.Prepare != nil {
		if err := s.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ctx.Effect
}

func stats(p skills.CastPlan) []skills.StatValue {
	b := p.Self
	if b == nil {
		b = p.Party
	}
	if b == nil {
		return nil
	}
	return b.Stats
}

// Every enabled 2300–2412 skill is registered or explicitly pending.
func TestEveryMPSkillIsRegisteredOrPending(t *testing.T) {
	seen := 0
	for _, job := range []int{2300, 2310, 2311, 2312, 2400, 2410, 2411, 2412} {
		for id := job * 10000; id < job*10000+10000; id++ {
			def, err := content.SkillDefinition("", id)
			if err != nil || def.Disabled {
				continue
			}
			seen++
			_, registered := skills.Lookup(id)
			if _, wait := pending[id]; registered == wait {
				t.Errorf("skill %d: registered=%v pending=%v", id, registered, wait)
			}
		}
	}
	if seen != 66 {
		t.Errorf("saw %d skills, inventory has 66", seen)
	}
}

func TestBuffsAtMaxLevel(t *testing.T) {
	merc := character(2312, dualBowgunID)
	phantom := character(2412, caneID)
	cases := []struct {
		id, level int
		c         domain.Character
		want      []skills.StatValue
		duration  time.Duration
	}{
		{23101002, 20, merc, []skills.StatValue{{Stat: "BOOSTER", Value: -2}}, 200 * time.Second},
		{23101003, 15, merc, []skills.StatValue{{Stat: "SPIRIT_SURGE", Value: 15}}, 180 * time.Second},
		{23111004, 20, merc, []skills.StatValue{{Stat: "ANGEL_ATK", Value: 40}}, 180 * time.Second},
		{23111005, 20, merc, []skills.StatValue{{Stat: "ABNORMAL_STATUS_R", Value: 25}, {Stat: "ELEMENTAL_STATUS_R", Value: 25}, {Stat: "WATER_SHIELD", Value: 30}}, 180 * time.Second},
		{23121004, 30, merc, []skills.StatValue{{Stat: "PIRATES_REVENGE", Value: 30}, {Stat: "ENHANCED_MAXHP", Value: 1500}}, 210 * time.Second},
		{23121005, 30, merc, []skills.StatValue{{Stat: "MAPLE_WARRIOR", Value: 15}}, 900 * time.Second},
		{24101005, 20, phantom, []skills.StatValue{{Stat: "BOOSTER", Value: -2}}, 210 * time.Second},
		{24111003, 20, phantom, []skills.StatValue{{Stat: "MAXHP", Value: 30}, {Stat: "MAXMP", Value: 30}, {Stat: "ABNORMAL_STATUS_R", Value: 40}, {Stat: "ELEMENTAL_STATUS_R", Value: 40}}, 180 * time.Second},
		{24111005, 20, phantom, []skills.StatValue{{Stat: "ANGEL_ATK", Value: 20}, {Stat: "ANGEL_ACC", Value: 200}}, 180 * time.Second},
		{24121004, 30, phantom, []skills.StatValue{{Stat: "ENHANCED_WATK", Value: 30}}, 210 * time.Second},
		{24121008, 30, phantom, []skills.StatValue{{Stat: "MAPLE_WARRIOR", Value: 15}}, 900 * time.Second},
	}
	for _, c := range cases {
		p := mustCast(t, c.id, c.level, c.c)
		if got := stats(p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("skill %d stats %+v want %+v", c.id, got, c.want)
		}
		b := p.Self
		if b == nil {
			b = p.Party
		}
		if b.Duration != c.duration {
			t.Errorf("skill %d duration %v want %v", c.id, b.Duration, c.duration)
		}
		if p.MPCost <= 0 {
			t.Errorf("skill %d costs no MP", c.id)
		}
		if _, err := buffstat.Resolve(statNames(c.want)); err != nil {
			t.Errorf("skill %d: %v", c.id, err)
		}
	}
}

func statNames(v []skills.StatValue) []buffstat.Stat {
	var out []buffstat.Stat
	for _, s := range v {
		out = append(out, s.Stat)
	}
	return out
}

func TestBoostersNeedTheirWeapon(t *testing.T) {
	if _, err := cast(t, 23101002, 20, character(2312, caneID)); err == nil {
		t.Error("dual bowguns boost with a cane")
	}
	if _, err := cast(t, 24101005, 20, character(2412, dualBowgunID)); err == nil {
		t.Error("cane booster with dual bowguns")
	}
}

func TestPartyBuffsUseTheWZArea(t *testing.T) {
	for _, id := range []int{23121005, 24121008} {
		p := mustCast(t, id, 30, character(id/10000, 0))
		if p.Self != nil || p.Party == nil || p.Area == nil || *p.Area != (skills.Rect{Left: -400, Top: -300, Right: 400, Bottom: 300}) {
			t.Errorf("skill %d party plan %+v", id, p)
		}
		h, _ := skills.Lookup(id)
		if h.Kind&skills.KindPartyBuff == 0 {
			t.Errorf("skill %d not a party skill", id)
		}
	}
}

func TestSpecialCasts(t *testing.T) {
	for _, id := range []int{23001002, 24001002} {
		p := mustCast(t, id, 10, character(id/10000, 0))
		if p.Self != nil || p.Party != nil || p.MPCost <= 0 {
			t.Errorf("skill %d is not cost only: %+v", id, p)
		}
	}
	for _, id := range []int{23121008, 24121009} {
		p := mustCast(t, id, 5, character(id/10000, 0))
		if !p.Dispel || p.Cooldown != 360*time.Second {
			t.Errorf("hero's will %d: %+v", id, p)
		}
	}
	p := mustCast(t, 24121007, 20, character(2412, caneID))
	if p.Cooldown != 120*time.Second || p.Self != nil {
		t.Errorf("soul steal %+v", p)
	}
	if mc := spec(t, 24121007).MobCast; mc == nil || !mc.Dispel || !mc.UseBox {
		t.Errorf("soul steal mob cast %+v", mc)
	}
}

func TestPassives(t *testing.T) {
	merc := character(2312, dualBowgunID)
	phantom := character(2412, caneID)
	cases := []struct {
		id, level int
		c         domain.Character
		want      skills.Bonus
	}{
		{23000001, 15, merc, skills.Bonus{AvoidRate: 20}},
		{23000003, 20, merc, skills.Bonus{Critical: 40}},
		{23100005, 20, merc, skills.Bonus{Mastery: 50, ACC: 120}},
		{23100005, 20, phantom, skills.Bonus{}},
		{23100008, 10, merc, skills.Bonus{STR: 30, DEX: 30}},
		{23111004, 20, merc, skills.Bonus{AvoidRate: 20}},
		{23120009, 30, merc, skills.Bonus{Mastery: 70, WATK: 30, CriticalDamageMin: 15}},
		{23120010, 20, merc, skills.Bonus{IgnoreDefense: 40}},
		{23120012, 30, merc, skills.Bonus{WATK: 20, ACCPercent: 10}},
		{23121004, 30, merc, skills.Bonus{AvoidRate: 20}},
		{24000003, 20, phantom, skills.Bonus{AvoidRate: 30}},
		{24001002, 20, phantom, skills.Bonus{Speed: 35, Jump: 20, SpeedLimitBonus: 20}},
		{24100004, 20, phantom, skills.Bonus{Mastery: 50, ACC: 200}},
		{24100006, 10, phantom, skills.Bonus{LUK: 40}},
		{24110007, 20, phantom, skills.Bonus{Critical: 50, CriticalDamageMin: 10}},
		{24111002, 20, phantom, skills.Bonus{LUK: 40}},
		{24120006, 30, phantom, skills.Bonus{Mastery: 70, WATK: 30, CriticalDamageMin: 15}},
	}
	for _, c := range cases {
		if got := passive(t, c.id, c.level, c.c); got != c.want {
			t.Errorf("skill %d passive %+v want %+v", c.id, got, c.want)
		}
	}
	// The expert replaces the base mastery.
	expert := character(2312, dualBowgunID, domain.SkillState{ID: 23120009, Level: 1})
	if got := passive(t, 23100005, 20, expert); got != (skills.Bonus{ACC: 120}) {
		t.Errorf("base mastery with expert %+v", got)
	}
}

func TestBuffDependentPassives(t *testing.T) {
	active := func(id int) domain.Character {
		c := character(id/10000, dualBowgunID)
		c.SkillBuffs = []domain.SkillBuff{{ID: id, Stat: "X", Value: 1, ExpiresAt: now.Add(time.Minute)}}
		return c
	}
	if got := passive(t, 23101003, 15, character(2312, 0)); got != (skills.Bonus{}) {
		t.Errorf("spirit surge without buff %+v", got)
	}
	if got := passive(t, 23101003, 15, active(23101003)); got != (skills.Bonus{Critical: 15, DamagePercent: 15, BossDamagePercent: 15}) {
		t.Errorf("spirit surge %+v", got)
	}
	if got := passive(t, 23121004, 30, active(23121004)); got != (skills.Bonus{AvoidRate: 20, DamagePercent: 30, BossDamagePercent: 30}) {
		t.Errorf("ancient warding %+v", got)
	}
}

func TestAttacksFireWithoutAmmo(t *testing.T) {
	for _, id := range skills.IDs() {
		if id/1000000 != 23 && id/1000000 != 24 {
			continue
		}
		h, _ := skills.Lookup(id)
		if h.Attack == nil || h.Attack.Kind&(combat.Melee|combat.Shoot) == 0 {
			continue
		}
		if h.Attack.Ammo != combat.AmmoNone {
			t.Errorf("skill %d consumes ammo", id)
		}
		def, _ := values(t, id, 1)
		if _, err := combat.FromSkill(def, def.MaxLevel); err != nil {
			t.Errorf("skill %d: %v", id, err)
		}
	}
}

func TestAttackRules(t *testing.T) {
	merc := character(2312, dualBowgunID)
	// Final attacks hit 6 targets (Java MapleStatEffect).
	for _, id := range []int{23100006, 23120012, 24100003} {
		if e := prepared(t, id, 20, merc); e.MobCount != 6 {
			t.Errorf("final attack %d mobCount %d", id, e.MobCount)
		}
	}
	// Rising Rush: 23120011 replaces 23110006's damR (Java damageIncrease.put).
	base := prepared(t, 23101001, 15, merc).Damage
	with3rd := prepared(t, 23101001, 15, character(2311, 0, domain.SkillState{ID: 23110006, Level: 15})).Damage
	with4th := prepared(t, 23101001, 15, character(2312, 0, domain.SkillState{ID: 23110006, Level: 15}, domain.SkillState{ID: 23120011, Level: 20})).Damage
	if base != 130 || with3rd != 130+30 || with4th != 130+50 {
		t.Errorf("rising rush damage %d %d %d", base, with3rd, with4th)
	}
	if d := prepared(t, 23101007, 20, character(2312, 0, domain.SkillState{ID: 23120011, Level: 20})).Damage; d != 220+50 {
		t.Errorf("rising rush kick damage %d", d)
	}
	boosts := []struct{ attack, passive, level, damR int }{
		{23111000, 23121000, 30, 25},
		{23111001, 23121002, 20, 30},
		{24101002, 24111006, 20, 50},
		{24111006, 24121003, 20, 50},
		{24111008, 24121003, 20, 50},
	}
	for _, b := range boosts {
		def, _ := values(t, b.attack, 1)
		plain := prepared(t, b.attack, def.MaxLevel, merc).Damage
		got := prepared(t, b.attack, def.MaxLevel, character(2412, 0, domain.SkillState{ID: b.passive, Level: b.level})).Damage
		if got-plain != b.damR {
			t.Errorf("skill %d boosted by %d: +%d want +%d", b.attack, b.passive, got-plain, b.damR)
		}
	}
	if s := spec(t, 23111000); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobStun {
		t.Errorf("lightning edge stun %+v", s.OnHit)
	}
	if s := spec(t, 23111002); len(s.OnHit) != 1 || s.OnHit[0] != (combat.MobStatusApply{Stat: combat.MobWeakness, ValueFrom: "x"}) {
		t.Errorf("unicorn spike imprint %+v", s.OnHit)
	}
	if s := spec(t, 23121002); len(s.OnHit) != 1 || s.OnHit[0] != (combat.MobStatusApply{Stat: combat.MobPDR, ValueFrom: "x"}) {
		t.Errorf("spikes royale %+v", s.OnHit)
	}
	if spec(t, 23121003).MaxDamage == nil {
		t.Error("lightning edge has no Java ceiling")
	}
	for _, id := range []int{24121000, 24121005} {
		if spec(t, id).Kind != combat.Melee {
			t.Errorf("skill %d is not close range", id)
		}
	}
}

func TestLightningEdgeCeiling(t *testing.T) {
	// Pins the stock ceiling; long damage is on by default.
	previousCap := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previousCap) })
	protocol.SetLongDamageCap(0)
	f := spec(t, 23121003).MaxDamage
	mob := content.Mob{HP: 5000000}
	dc := combat.DamageContext{Mob: mob, Effect: combat.Effect{Damage: 100}, Stats: combat.PlayerStats{WATK: 10}}
	if got := f(dc); got != combat.MaxLineDamage() {
		t.Errorf("non-boss ceiling %d", got)
	}
	mob.Boss = true
	dc.Mob = mob
	if got := f(dc); got != combat.LineCeiling(dc) {
		t.Errorf("boss ceiling %d", got)
	}
}

func TestSummons(t *testing.T) {
	p := mustCast(t, 23111008, 20, character(2311, dualBowgunID))
	if want := []skills.StatValue{{Stat: "SUMMON", Value: 1}}; !reflect.DeepEqual(stats(p), want) || p.Self.Duration != 160*time.Second || p.Cooldown != 120*time.Second || p.MPCost != 60 {
		t.Errorf("elemental knights cast %+v", p)
	}
	for _, id := range []int{23111008, 23111009, 23111010} {
		s := spec(t, id)
		if s.Kind != combat.Summoned || s.Summon == nil || s.Summon.MoveAbility != 4 || s.Summon.AssistType != 1 || s.Prepare == nil {
			t.Errorf("knight %d spec %+v", id, s)
		}
		if h, _ := skills.Lookup(id); (h.Cast != nil) != (id == 23111008) {
			t.Errorf("knight %d castable=%v", id, h.Cast != nil)
		}
	}
	if v := spec(t, 23111008).Summon.Variants; !reflect.DeepEqual(v, []int{23111008, 23111009, 23111010}) {
		t.Errorf("knight variants %v", v)
	}
	def, _ := values(t, 23111008, 20)
	base, err := combat.FromSkill(def, 20)
	if err != nil {
		t.Fatal(err)
	}
	knight := func(variant int) combat.Effect {
		ctx := &combat.AttackContext{Kind: combat.Summoned, Effect: base, SummonSkillID: variant}
		if err := spec(t, 23111008).Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		return ctx.Effect
	}
	// Level 20: damage 550, prop 90, subTime 5; v 550, w 5, x 1, y 110; z 625.
	if e := knight(23111008); e.Damage != 550 || e.Prop != 90 || !reflect.DeepEqual(e.MonsterStatus, []combat.MobStatusApply{{Stat: combat.MobFreeze, Value: 1, SubTime: true}}) {
		t.Errorf("ice knight %+v", e)
	}
	if e := knight(23111009); e.Damage != 550 || e.Element != combat.Fire || e.Prop != 0 || e.DOT != 110 || e.DOTTime != 5*time.Second || e.DOTInterval != time.Second || len(e.MonsterStatus) != 1 || !e.MonsterStatus[0].Poison {
		t.Errorf("fire knight %+v", e)
	}
	if e := knight(23111010); e.Damage != 625 || len(e.MonsterStatus) != 0 {
		t.Errorf("dark knight %+v", e)
	}
	if e := knight(0); e.Damage != 625 || len(e.MonsterStatus) != 0 {
		t.Errorf("unknown knight %+v", e)
	}
}

func TestLinkedAttacksUseTheParentLevel(t *testing.T) {
	for id, parent := range map[int]int{23101007: 23101001, 24111008: 24111006, 24121010: 24121003} {
		h, _ := skills.Lookup(id)
		if h.LevelFrom != parent || h.Attack.LevelFrom != parent {
			t.Errorf("skill %d links %d/%d want %d", id, h.LevelFrom, h.Attack.LevelFrom, parent)
		}
	}
}
