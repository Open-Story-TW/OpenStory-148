package resist1

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

var now = time.Unix(1000, 0)

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

func castAs(t *testing.T, caster domain.Character, id, level int) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	return h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: caster, Now: now})
}

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	p, err := castAs(t, demon(3112), id, level)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func demon(job int) domain.Character {
	return domain.Character{Job: job, Level: 150, HP: 5000, MaxHP: 5000, MP: 120, MaxMP: 120}
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

func passive(t *testing.T, id, level int, c domain.Character) skills.Bonus {
	t.Helper()
	h, _ := skills.Lookup(id)
	def, v := values(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c, Now: now})
}

func withSkill(c domain.Character, id, level int) domain.Character {
	c.Skills = append(c.Skills, domain.SkillState{ID: id, Level: level})
	return c
}

func withBuff(c domain.Character, id, level int, s string, v int) domain.Character {
	c.SkillBuffs = append(c.SkillBuffs, domain.SkillBuff{ID: id, Level: level, Stat: s, Value: v, ExpiresAt: now.Add(time.Hour)})
	return c
}

// Every R1 skill of the plan inventory is registered here.
func TestRegistersEveryInventorySkill(t *testing.T) {
	raw, err := os.ReadFile("../testdata/skill-plan-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var inv struct {
		Skills []struct {
			ID     int    `json:"id"`
			Thread string `json:"thread"`
		} `json:"skills"`
	}
	if err = json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	want := map[int]bool{}
	for _, s := range inv.Skills {
		if s.Thread == "R1" {
			want[s.ID] = true
			if _, ok := skills.Lookup(s.ID); !ok {
				t.Errorf("%d not registered", s.ID)
			}
		}
	}
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) == "resist1" && !want[id] {
			t.Errorf("%d registered but not in the inventory", id)
		}
	}
	if len(want) != 77 {
		t.Fatalf("inventory has %d R1 skills", len(want))
	}
}

// Every castable R1 skill plans at every level with sendable stats; every
// attack has damage.
func TestEveryHandlerPlansSendableStats(t *testing.T) {
	caster := demon(3112)
	for _, a := range []int{32001003, 32111012, 32101003} {
		caster = withBuff(caster, a, 20, "X", 1) // Body Boost needs an aura
	}
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "resist1" {
			continue
		}
		h, _ := skills.Lookup(id)
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatal(err)
		}
		if h.Attack != nil && h.Attack.Kind != 0 {
			if _, v := values(t, id, def.MaxLevel); v["damage"] < 1 {
				t.Errorf("%d: attack without damage", id)
			}
		}
		if h.Cast == nil {
			continue
		}
		for level := 1; level <= def.MaxLevel; level++ {
			p, err := castAs(t, caster, id, level)
			if err != nil {
				t.Fatalf("%d@%d: %v", id, level, err)
			}
			if p.MPCost < 0 || p.HPCost < 0 || p.Cooldown < 0 {
				t.Errorf("%d@%d: negative cost %+v", id, level, p)
			}
			b := p.Self
			if b == nil {
				b = p.Party
			}
			if (h.Kind&skills.KindPartyBuff != 0) != (p.Party != nil) {
				t.Errorf("%d: party kind and plan differ", id)
			}
			if b == nil {
				if !p.Dispel && h.Kind&(skills.KindSpecial|skills.KindFieldObject) == 0 {
					t.Errorf("%d@%d: plan does nothing", id, level)
				}
				continue
			}
			if len(b.Stats) == 0 || b.Duration < time.Second || b.Duration > skills.Permanent {
				t.Errorf("%d@%d: buff %+v", id, level, b)
			}
			names := make([]buffstat.Stat, len(b.Stats))
			for i, s := range b.Stats {
				names[i] = s.Stat
				if s.Value < -32768 || s.Value > 32767 {
					t.Errorf("%d@%d: %s value %d", id, level, s.Stat, s.Value)
				}
			}
			if _, err := buffstat.Resolve(names); err != nil {
				t.Errorf("%d@%d: %v", id, level, err)
			}
		}
	}
}

func TestDemonForceCosts(t *testing.T) {
	p := cast(t, 31121002, 10) // Vampiric Touch
	if p.MPCost != 50 || p.Party == nil || stats(p.Party)["COMBO_DRAIN"] != 3 || p.Party.Duration != 180*time.Second || p.Area == nil || p.Area.Left != -512 {
		t.Fatalf("vampiric touch %+v %+v", p, p.Party)
	}
	p = cast(t, 31121007, 15)
	if p.MPCost != 120 || p.Cooldown != 300*time.Second || stats(p.Self)["BOUNDLESS_RAGE"] != 1 || p.Self.Duration != 30*time.Second {
		t.Fatalf("boundless rage %+v %+v", p, p.Self)
	}
	raging := withBuff(demon(3112), 31121007, 15, "BOUNDLESS_RAGE", 1)
	if p, _ = castAs(t, raging, 31121005, 30); p.MPCost != 0 || p.HPCost != 300 || stats(p.Self)["PIRATES_REVENGE"] != 35 || stats(p.Self)["DARK_METAMORPHOSIS"] != 6 || p.Self.Duration != 180*time.Second {
		t.Fatalf("metamorphosis under boundless rage %+v %+v", p, p.Self)
	}
	p = cast(t, 31001001, 20)
	if p.MPCost != 0 || p.HPCost != 80 || stats(p.Self)["BOOSTER"] != -2 || p.Self.Duration != 180*time.Second {
		t.Fatalf("demon booster %+v %+v", p, p.Self)
	}
	p = cast(t, 31101003, 20)
	if p.HPCost != 150 || stats(p.Self)["PERFECT_ARMOR"] != 400 || p.Self.Duration != 180*time.Second {
		t.Fatalf("dark revenge %+v %+v", p, p.Self)
	}
	p = cast(t, 31111004, 20)
	if s := stats(p.Self); s["DEFENCE_BOOST_R"] != 100 || s["ABNORMAL_STATUS_R"] != 80 || s["ELEMENTAL_STATUS_R"] != 80 || p.HPCost != 200 {
		t.Fatalf("black hearted strength %+v %+v", p, p.Self)
	}
	p = cast(t, 31121004, 30)
	if p.Party == nil || stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.HPCost != 500 || p.Party.Duration != 900*time.Second {
		t.Fatalf("maple warrior %+v %+v", p, p.Party)
	}
}

func TestDemonLashForceGainAndBonus(t *testing.T) {
	defer func(f func(int) bool) { rollProp = f }(rollProp)
	rollProp = func(int) bool { return true }
	h, _ := skills.Lookup(31000004)
	def, v := values(t, 31000004, 10)
	trigger := func(c domain.Character, skill int, targets ...int32) *skills.BuffChange {
		return h.OnAttack(&skills.TriggerContext{Skill: def, Level: 10, Values: v, Character: c, Attack: &combat.AttackEvent{SkillID: skill, Targets: targets}, Now: now})
	}
	c := demon(3100)
	c.Level = 25
	if ch := trigger(c, 31000004, 1, 2, 3); ch == nil || ch.HealMP != 3 {
		t.Fatalf("level 25 gain %+v", ch)
	}
	c.Level = 125
	if ch := trigger(withSkill(c, 31110009, 5), 31001008, 1, 2); ch == nil || ch.HealMP != 10 {
		t.Fatalf("level 125 with improving demon force %+v", ch)
	}
	if ch := trigger(c, 31001000, 1); ch != nil {
		t.Fatalf("grim scythe gains no force %+v", ch)
	}
	if ch := trigger(c, 31000004); ch != nil {
		t.Fatalf("a miss gains no force %+v", ch)
	}

	ctx := &combat.AttackContext{Character: withSkill(withSkill(c, 31100007, 1), 31110010, 1), Effect: combat.Effect{SkillID: 31000004, Damage: 100}, Now: now}
	if err := lashBonus(ctx); err != nil || ctx.Effect.Damage != 220 {
		t.Fatalf("2nd enhancement %d %v", ctx.Effect.Damage, err)
	}
	ctx = &combat.AttackContext{Character: withSkill(c, 31100007, 1), Effect: combat.Effect{Damage: 100}, Now: now}
	if lashBonus(ctx); ctx.Effect.Damage != 140 {
		t.Fatalf("1st enhancement %d", ctx.Effect.Damage)
	}
	ctx = &combat.AttackContext{Character: withSkill(c, 31120011, 1), Effect: combat.Effect{Damage: 100}, Now: now}
	if lashBonus(ctx); ctx.Effect.Damage != 380 {
		t.Fatalf("final enhancement %d", ctx.Effect.Damage)
	}
}

func TestDemonAttackStatuses(t *testing.T) {
	spec := func(id int) *combat.AttackSpec {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil {
			t.Fatalf("%d not an attack", id)
		}
		return h.Attack
	}
	for _, id := range []int{31101002, 31111001} {
		if s := spec(id); s.Kind != combat.Melee || len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobStun {
			t.Errorf("%d stun %+v", id, s.OnHit)
		}
	}
	if s := spec(31111005); len(s.OnHit) != 1 || !s.OnHit[0].Poison {
		t.Errorf("carrion breath %+v", s.OnHit)
	}
	if s := spec(31121006); len(s.OnHit) != 2 || s.OnHit[0].Stat != combat.MobWeb || !s.OnHit[1].Poison {
		t.Errorf("binding darkness %+v", s.OnHit)
	}
	if b := passive(t, 31121006, 20, demon(3112)); b.IgnoreDefense != 30 {
		t.Errorf("binding darkness passive %+v", b)
	}
	ctx := &combat.AttackContext{Effect: combat.Effect{SkillID: 31121003, Level: 20}}
	if err := demonCry(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 6 {
		t.Fatalf("demon cry %+v %v", ctx.Effect.MonsterStatus, err)
	}
	got := map[combat.MobStat]int{}
	for _, s := range ctx.Effect.MonsterStatus {
		got[s.Stat] = s.Value
		if !s.SubTime {
			t.Errorf("demon cry %v uses time", s.Stat)
		}
	}
	if got[combat.MobPAD] != -15 || got[combat.MobPDR] != -15 || got[combat.MobACC] != -20 || got[combat.MobShowdown] != 20 {
		t.Fatalf("demon cry values %v", got)
	}
	actx := &combat.AttackContext{Character: domain.Character{MaxHP: 1000}, Effect: combat.Effect{X: 40}}
	if hp, _ := bloodCrow(actx, 10000); hp != 500 {
		t.Fatalf("blood crow drain capped %d", hp)
	}
	if hp, _ := bloodCrow(actx, 1000); hp != 400 {
		t.Fatalf("blood crow drain %d", hp)
	}
}

func TestDarkRevengeBounces(t *testing.T) {
	h, _ := skills.Lookup(31101003)
	def, v := values(t, 31101003, 20)
	rows := []domain.SkillBuff{{ID: 31101003, Stat: "PERFECT_ARMOR", Value: 400, ExpiresAt: now.Add(time.Minute)}}
	ch := h.OnDamaged(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Rows: rows, Damage: 50, Mob: 7, Now: now})
	if ch == nil || len(ch.MobHits) != 1 || ch.MobHits[0] != (combat.ServerHit{ObjectID: 7, Damage: 200}) || len(ch.MobCast) != 1 {
		t.Fatalf("bounce %+v", ch)
	}
	if ch = h.OnDamaged(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Damage: 50, Mob: 7, Now: now}); ch != nil {
		t.Fatalf("bounce without the buff %+v", ch)
	}
	if s := h.Attack.MobCast; s == nil || len(s.Statuses) != 1 || s.Statuses[0].Stat != combat.MobStun || !s.Statuses[0].SubTime {
		t.Fatalf("dark revenge stun %+v", s)
	}
}

func TestDemonPassives(t *testing.T) {
	axe := demon(3112)
	axe.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1312000}}
	if b := passive(t, 31100004, 20, axe); b != (skills.Bonus{Mastery: 50, ACC: 120}) {
		t.Errorf("weapon mastery %+v", b)
	}
	if b := passive(t, 31100004, 20, demon(3112)); b != (skills.Bonus{}) {
		t.Errorf("weapon mastery bare-handed %+v", b)
	}
	if b := passive(t, 31000002, 10, axe); b != (skills.Bonus{ACC: 200, Speed: 25, Jump: 20}) {
		t.Errorf("dark agility %+v", b)
	}
	rh, _ := skills.Lookup(31110009)
	def, v := values(t, 31110009, 5)
	low := demon(3112)
	low.MP = 10
	if ch := rh.OnTick(&skills.TriggerContext{Skill: def, Level: 5, Values: v, Character: low, Now: now}); rh.TickEvery != 4*time.Second || ch == nil || ch.HealMP != 10 {
		t.Errorf("improving demon force regen %v %+v", rh.TickEvery, ch)
	}
	low.Job = 3110
	if ch := rh.OnTick(&skills.TriggerContext{Skill: def, Level: 5, Values: v, Character: low, Now: now}); ch != nil {
		t.Errorf("improving demon force regen before 3111 %+v", ch)
	}
	if b := passive(t, 31120009, 30, axe); b.WDEFPercent != 20 {
		t.Errorf("tough skin %+v", b)
	}
}

func TestDemonCeilingAndComboDrain(t *testing.T) {
	c := withBuff(domain.Character{}, 31121005, 30, "PIRATES_REVENGE", 35)
	c.SkillBuffs[0].ExpiresAt = time.Now().Add(time.Hour)
	if got := demonCeiling(combat.DamageContext{Attacker: c}, 1000); got != 1350 {
		t.Fatalf("metamorphosis ceiling %d", got)
	}
	c = domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 32001003, Stat: "DARK_AURA", Value: 20, ExpiresAt: time.Now().Add(time.Hour)}, {ID: 32111005, Level: 20, Stat: "BODY_BOOST", Value: 20, ExpiresAt: time.Now().Add(time.Hour)}}}
	if got := demonCeiling(combat.DamageContext{Attacker: c}, 1000); got != 1200*140/100 {
		t.Fatalf("dark aura and body boost ceiling %d", got)
	}
	if got := demonCeiling(combat.DamageContext{Attacker: domain.Character{}}, 1000); got != 1000 {
		t.Fatalf("plain ceiling %d", got)
	}
	member := withBuff(domain.Character{MaxHP: 1000}, 31121002, 10, "COMBO_DRAIN", 3)
	if hp, _ := comboDrain(&combat.AttackContext{Character: member, Now: now}, 10000); hp != 300 {
		t.Fatalf("vampiric touch drain %d", hp)
	}
	if hp, _ := comboDrain(&combat.AttackContext{Character: member, Now: now}, 100000); hp != 500 {
		t.Fatalf("vampiric touch drain cap %d", hp)
	}
	if hp, _ := comboDrain(&combat.AttackContext{Character: domain.Character{MaxHP: 1000}, Now: now}, 10000); hp != 0 {
		t.Fatalf("drain without the buff %d", hp)
	}
}

func TestAuras(t *testing.T) {
	mage := demon(3212)
	p := cast(t, 32001003, 20)
	if p.Party == nil || !p.Toggle || stats(p.Party)["DARK_AURA"] != 10 || p.Party.Duration != skills.Permanent || p.MPCost != 30 || p.Area == nil || p.Area.Left != -300 {
		t.Fatalf("dark aura %+v %+v", p, p.Party)
	}
	if p, _ = castAs(t, withSkill(mage, 32120000, 30), 32001003, 20); stats(p.Party)["DARK_AURA"] != 20 || p.MPCost != 30 {
		t.Fatalf("advanced dark aura %+v %+v", p, p.Party)
	}
	if p, _ = castAs(t, withSkill(mage, 32110000, 10), 32111012, 20); stats(p.Party)["BLUE_AURA"] != 10 || p.MPCost != 42 {
		t.Fatalf("advanced blue aura %+v %+v", p, p.Party)
	}
	if p = cast(t, 32101003, 20); stats(p.Party)["YELLOW_AURA"] != 20 {
		t.Fatalf("yellow aura %+v", p.Party)
	}
	if _, err := castAs(t, mage, 32111005, 20); err == nil {
		t.Fatal("body boost without an aura")
	}
	if len(p.Replace) != 6 || p.Replace[0] != 32001003 {
		t.Fatalf("yellow aura replaces %v", p.Replace)
	}
	if p, _ = castAs(t, withBuff(mage, 32111012, 20, "BLUE_AURA", 20), 32111005, 20); p.Self == nil || p.Self.Duration != 10*time.Second || stats(p.Self)["BODY_BOOST"] != 20 || p.Cooldown != 120*time.Second || len(p.Retime) != 1 || p.Retime[0] != (skills.SkillDuration{SkillID: 32111012, Duration: 10 * time.Second}) {
		t.Fatalf("body boost over blue %+v %+v", p, p.Self)
	}
	if p, _ = castAs(t, withBuff(mage, 32001003, 20, "DARK_AURA", 10), 32111005, 20); p.Self.Duration != 60*time.Second {
		t.Fatalf("body boost over dark %+v", p.Self)
	}
	if b := passive(t, 32110000, 10, mage); b.ASR != 20 || b.TER != 20 {
		t.Errorf("advanced blue aura passive %+v", b)
	}
}

func TestBattleMageCasts(t *testing.T) {
	p := cast(t, 32111010, 10)
	if p.MPCost != 20 || !p.Toggle || stats(p.Self)["TELEPORT_MASTERY"] != 20 || p.Self.Duration != skills.Permanent {
		t.Fatalf("teleport mastery %+v %+v", p, p.Self)
	}
	if p = cast(t, 32121003, 30); stats(p.Self)["TORNADO"] != 1 || p.Self.Duration != 20*time.Second || p.Cooldown != 40*time.Second {
		t.Fatalf("cyclone %+v %+v", p, p.Self)
	}
	if p = cast(t, 32111004, 10); stats(p.Self)["CONVERSION"] != 100 || p.MPCost != 1000 {
		t.Fatalf("conversion %+v %+v", p, p.Self)
	}
	if p = cast(t, 32111006, 20); stats(p.Self)["REAPER"] != 1 || p.Self.Duration != 120*time.Second {
		t.Fatalf("reaper %+v", p.Self)
	}
	if p = cast(t, 32121006, 30); p.Self != nil || p.Cooldown != 420*time.Second || p.MPCost != 150 {
		t.Fatalf("party shield %+v", p)
	}
	if h, _ := skills.Lookup(32121006); h.Attack == nil || h.Attack.Field == nil || !h.Attack.Field.PlayersInvincible {
		t.Fatalf("party shield field %+v", h.Attack)
	}
	if p = cast(t, 32121008, 5); !p.Dispel {
		t.Fatalf("hero's will %+v", p)
	}
	h, _ := skills.Lookup(32121003)
	ctx := &combat.AttackContext{Character: demon(3212), Now: now}
	if err := h.Attack.Prepare(ctx); err == nil {
		t.Fatal("cyclone body attack without TORNADO")
	}
	ctx.Character = withBuff(ctx.Character, 32121003, 30, "TORNADO", 1)
	ctx.Effect.MPCon = 170
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.MPCon != 0 {
		t.Fatalf("cyclone hit %+v %v", ctx.Effect, err)
	}
}

func TestBattleMageAttacks(t *testing.T) {
	h, _ := skills.Lookup(32121002)
	ctx := &combat.AttackContext{Effect: combat.Effect{Damage: 180}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.Damage != 270 || h.Attack.Kind != combat.Melee|combat.Magic {
		t.Fatalf("finishing blow %d %v", ctx.Effect.Damage, err)
	}
	h, _ = skills.Lookup(32111003)
	ctx = &combat.AttackContext{Character: withSkill(demon(3212), 32121004, 30), Effect: combat.Effect{Damage: 1040}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.Damage != 1040*160/100 {
		t.Fatalf("dark shock with dark genesis %d %v", ctx.Effect.Damage, err)
	}
	staffUser := demon(3212)
	staffUser.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1382000}}
	if b := passive(t, 32100006, 20, staffUser); b != (skills.Bonus{Mastery: 50, MATK: 10, Critical: 40}) {
		t.Errorf("staff mastery %+v", b)
	}
	if b := passive(t, 32120009, 30, staffUser); b.MaxHPPercent != 20 || b.MaxMPPercent != 20 {
		t.Errorf("energize %+v", b)
	}
}

func TestBatchTwoCoreWiring(t *testing.T) {
	defer func(f func(int) bool) { rollProp = f }(rollProp)
	rollProp = func(int) bool { return true }
	for id, from := range map[int]int{31001006: 31000004, 31001007: 31000004, 31001008: 31000004, 31121010: 31121000, 32001008: 32001001, 32001011: 32001001} {
		if h, _ := skills.Lookup(id); h.Attack == nil || h.Attack.LevelFrom != from {
			t.Errorf("%d level from %+v", id, h.Attack)
		}
	}

	// Boundless Rage clears the DF cost of attacks.
	h, _ := skills.Lookup(31111000)
	ctx := &combat.AttackContext{Character: withBuff(demon(3112), 31121007, 15, "BOUNDLESS_RAGE", 1), Effect: combat.Effect{MPCon: 40, Damage: 300}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.MPCon != 0 {
		t.Fatalf("rage %+v %v", ctx.Effect, err)
	}
	ctx = &combat.AttackContext{Character: demon(3112), Effect: combat.Effect{MPCon: 40}, Now: now}
	if h.Attack.Prepare(ctx); ctx.Effect.MPCon != 40 {
		t.Fatalf("no rage %+v", ctx.Effect)
	}

	// Dark Revenge caps the bounce at a tenth of the attacker's max HP.
	rh, _ := skills.Lookup(31101003)
	def, v := values(t, 31101003, 20)
	rows := []domain.SkillBuff{{ID: 31101003, Stat: "PERFECT_ARMOR", Value: 400, ExpiresAt: now.Add(time.Minute)}}
	ch := rh.OnDamaged(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Rows: rows, Damage: 500, Mob: 7, Mobs: []skills.TriggerMob{{ObjectID: 7, MaxHP: 10000}}, Now: now})
	if ch == nil || ch.MobHits[0].Damage != 1000 {
		t.Fatalf("capped bounce %+v", ch)
	}

	// Power Guard refills HP and DF on a defType 1 hit.
	ph, _ := skills.Lookup(31110008)
	def, v = values(t, 31110008, 15)
	if ch = ph.OnDamaged(&skills.TriggerContext{Skill: def, Level: 15, Values: v, Character: demon(3112), DefType: 1, Now: now}); ch == nil || ch.HealHP != 5000/100*v["y"] || ch.HealMP != v["z"] {
		t.Fatalf("power guard %+v", ch)
	}
	if ch = ph.OnDamaged(&skills.TriggerContext{Skill: def, Level: 15, Values: v, Character: demon(3112), Now: now}); ch != nil {
		t.Fatalf("power guard on a plain hit %+v", ch)
	}

	// Demon Lash draws one orb per damaged monster with a running counter.
	var orbs [][3]int
	e := combat.AttackEvent{Session: domain.CharacterSession{CharacterID: 99, Character: demon(3112)}, Targets: []int32{5, 6}}
	e.GainForce = func(oid int32, count, color int) error {
		orbs = append(orbs, [3]int{int(oid), count, color})
		return nil
	}
	if lh, _ := skills.Lookup(31000004); lh.Attack.AfterHit == nil {
		t.Fatal("demon lash has no orbs")
	} else {
		lh.Attack.AfterHit(e)
	}
	if len(orbs) != 2 || orbs[0] != [3]int{5, 4, 1} || orbs[1] != [3]int{6, 4, 2} {
		t.Fatalf("orbs %v", orbs)
	}

	// Reapers spawn from attacks while REAPER runs, at most four.
	spawned := 0
	e = combat.AttackEvent{Session: domain.CharacterSession{Character: withBuff(demon(3212), 32111006, 20, "REAPER", 1)}, Targets: []int32{1, 2, 3, 4, 5, 6}, Now: now}
	e.SpawnSummon = func(id, level int) error {
		if id != 32111006 || level != 20 {
			t.Fatalf("spawn %d@%d", id, level)
		}
		spawned++
		return nil
	}
	e.SummonCount = func(int) int { return spawned }
	if s := reaperSpec.Summon; !s.NoCastSpawn || s.DurationKey != "x" || !s.Multi || s.MoveAbility != 2 {
		t.Fatalf("reaper spec %+v", s)
	}
	reaperSpawn(e)
	if spawned != maxReapers {
		t.Fatalf("spawned %d reapers", spawned)
	}
	spawned = 0
	e.Session.Character = demon(3212)
	if reaperSpawn(e); spawned != 0 {
		t.Fatalf("reaper without the buff")
	}
}

func TestAurasLeaveNonBattleMagesOnEntry(t *testing.T) {
	for _, id := range []int{32001003, 32111012, 32101003, 32110007, 32110008, 32110009} {
		h, _ := skills.Lookup(id)
		if h.DropOnEnter == nil || !h.DropOnEnter(domain.Character{Job: 3112}) || !h.DropOnEnter(domain.Character{Job: 0}) || h.DropOnEnter(domain.Character{Job: 3212}) || h.DropOnEnter(domain.Character{Job: 3200}) {
			t.Errorf("%d drop on enter", id)
		}
	}
}

func TestDemonChaseCastIsFree(t *testing.T) {
	h, _ := skills.Lookup(31101002)
	if h.Attack == nil || h.Attack.Kind != combat.Melee {
		t.Fatalf("demon chase attack %+v", h.Attack)
	}
	if p := cast(t, 31101002, 15); p.MPCost != 0 || p.HPCost != 0 || p.Self != nil || p.Party != nil {
		t.Fatalf("demon chase cast %+v", p)
	}
}

func TestMetamorphosisHitsAreFree(t *testing.T) {
	h, _ := skills.Lookup(31121005)
	ctx := &combat.AttackContext{Character: demon(3112), Effect: combat.Effect{SkillID: 31121005, MPCon: 75, HPCon: 300, Damage: 150}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.MPCon != 0 || ctx.Effect.HPCon != 0 || ctx.Effect.Damage != 150 {
		t.Fatalf("metamorphosis hit %+v %v", ctx.Effect, err)
	}
}
