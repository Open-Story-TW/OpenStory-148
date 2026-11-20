package aranevan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

var now = time.Unix(1_800_000_000, 0)

func def(t *testing.T, id, level int) (content.Skill, map[string]int) {
	t.Helper()
	d, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return d, v
}

func castAs(t *testing.T, id, level int, caster domain.Character) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	d, v := def(t, id, level)
	return h.Cast(&skills.CastContext{Skill: d, Level: level, Values: v, Caster: caster, Now: now})
}

// caster has the combo ability Combo Recharge needs.
var caster = domain.Character{Job: 2112, HP: 5000, MaxHP: 5000, MP: 5000, MaxMP: 5000, Skills: []domain.SkillState{{ID: 21000000, Level: 10}}}

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	p, err := castAs(t, id, level, caster)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

func spec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatalf("skill %d has no attack", id)
	}
	return h.Attack
}

// prepared runs the skill's Prepare on a context built from its WZ level.
func prepared(t *testing.T, id, level int, ch domain.Character) *combat.AttackContext {
	t.Helper()
	d, _ := def(t, id, level)
	e, err := combat.FromSkill(d, level)
	if err != nil {
		t.Fatal(err)
	}
	s := spec(t, id)
	ctx := &combat.AttackContext{Kind: s.Kind, Character: ch, Effect: e, Now: now}
	if s.Prepare != nil {
		if err = s.Prepare(ctx); err != nil {
			t.Fatalf("%d: %v", id, err)
		}
	}
	return ctx
}

func row(id, level int, stat string, v int, left time.Duration) domain.SkillBuff {
	return domain.SkillBuff{ID: id, Level: level, Stat: stat, Value: v, ExpiresAt: now.Add(left)}
}

// Every AE skill of the inventory is registered here; every castable one
// plans at every level with known buff stats.
func TestEveryAESkillIsRegisteredAndPlans(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "skill-plan-inventory.json"))
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
	want := 0
	for _, s := range inv.Skills {
		if s.Thread != "AE" {
			continue
		}
		want++
		if h, ok := skills.Lookup(s.ID); !ok || h.Package() != "aranevan" {
			t.Errorf("%d not registered by aranevan", s.ID)
		}
	}
	count := 0
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "aranevan" {
			continue
		}
		count++
		h, _ := skills.Lookup(id)
		d, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatalf("%d: %v", id, err)
		}
		if h.Attack != nil && h.Attack.Field == nil && h.Attack.Kind != 0 {
			if _, v := def(t, id, d.MaxLevel); v["damage"] < 1 {
				t.Errorf("%d: attack without damage", id)
			}
		}
		if h.Cast == nil {
			continue
		}
		for level := 1; level <= d.MaxLevel; level++ {
			p, err := castAs(t, id, level, caster)
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
				if h.Kind&(skills.KindSpecial|skills.KindFieldObject) == 0 && !p.Dispel {
					t.Errorf("%d@%d: plan does nothing", id, level)
				}
				continue
			}
			if len(b.Stats) == 0 || b.Duration < time.Second {
				t.Errorf("%d@%d: empty buff %+v", id, level, b)
			}
			for _, s := range b.Stats {
				if _, ok := buffstat.Lookup(s.Stat); !ok {
					t.Errorf("%d: unknown stat %s", id, s.Stat)
				}
			}
		}
	}
	if want != 77 || count != want {
		t.Fatalf("registered %d of %d AE skills", count, want)
	}
}

func TestAranBuffs(t *testing.T) {
	_, v := def(t, 21001003, 20)
	if p := cast(t, 21001003, 20); stats(p.Self)["BOOSTER"] != -v["y"] || v["y"] < 1 || p.Self.Duration != 200*time.Second {
		t.Fatalf("polearm booster %+v", p.Self)
	}
	p := cast(t, 21111001, 20)
	if s := stats(p.Self); s["SMART_KNOCKBACK"] != 10 || s["ENHANCED_WATK"] != 25 || s["ENHANCED_WDEF"] != 215 || s["ENHANCED_MDEF"] != 215 || p.MPCost != 30 {
		t.Fatalf("smart knockback %+v", p.Self)
	}
	p = cast(t, 21120007, 30)
	if p.Self != nil || p.Party == nil || p.Area == nil || p.Area.Left != -400 {
		t.Fatalf("combo barrier must be a party buff %+v", p)
	}
	if s := stats(p.Party); s["COMBO_BARRIER"] != 800 || s["WDEF"] != 360 || s["MDEF"] != 360 {
		t.Fatalf("combo barrier %+v", p.Party)
	}
	p = cast(t, 21121000, 30)
	if p.Party == nil || stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second {
		t.Fatalf("maple warrior %+v", p)
	}
	if p = cast(t, 21111012, 10); p.Party == nil || stats(p.Party)["WATK"] != 30 || stats(p.Party)["MATK"] != 30 {
		t.Fatalf("maha's blessing %+v", p)
	}
	if p = cast(t, 21121003, 20); stats(p.Self)["STANCE"] != 90 {
		t.Fatalf("freezing posture %+v", p.Self)
	}
	if p = cast(t, 21100005, 10); stats(p.Self)["COMBO_DRAIN"] != 10 || p.Self.Duration != 240*time.Second {
		t.Fatalf("combo drain %+v", p.Self)
	}
	if p = cast(t, 21121008, 5); !p.Dispel || p.Cooldown != 360*time.Second {
		t.Fatalf("hero's will %+v", p)
	}
	// Combo Recharge pays x% of the maximum HP besides mpCon.
	p, err := castAs(t, 21111009, 20, domain.Character{HP: 1000, MaxHP: 1000, Skills: caster.Skills})
	if err != nil || p.HPCost != 100 || p.MPCost != 50 || p.Cooldown != 30*time.Second || p.Self != nil {
		t.Fatalf("combo recharge %+v %v", p, err)
	}
	if _, err = castAs(t, 21111009, 20, domain.Character{HP: 1000, MaxHP: 1000}); err == nil {
		t.Fatal("combo recharge without combo ability")
	}
}

func TestEvanBuffs(t *testing.T) {
	p := cast(t, 22131001, 10)
	if p.Party == nil || p.Cooldown != 90*time.Second {
		t.Fatalf("magic shield %+v", p)
	}
	if s := stats(p.Party); s["MAGIC_SHIELD"] != 10 || s["WDEF"] != 300 || s["MDEF"] != 300 || s["MAXHP"] != 10 {
		t.Fatalf("magic shield %+v", p.Party)
	}
	if p = cast(t, 22131002, 10); stats(p.Self)["ELEMENT_RESET"] != 100 {
		t.Fatalf("elemental reset %+v", p.Self)
	}
	if p = cast(t, 22141002, 20); stats(p.Self)["BOOSTER"] != -2 {
		t.Fatalf("magic booster %+v", p.Self)
	}
	if p = cast(t, 22151003, 10); p.Party == nil || stats(p.Party)["MAGIC_RESISTANCE"] != 20 || stats(p.Party)["MDEF"] != 100 {
		t.Fatalf("magic resistance %+v", p)
	}
	if p = cast(t, 22161004, 15); p.Party == nil || stats(p.Party)["ONYX_SHROUD"] != 20 {
		t.Fatalf("onyx shroud %+v", p)
	}
	if p = cast(t, 22181000, 25); stats(p.Self)["ENHANCED_MATK"] != 35 || stats(p.Self)["ENHANCED_WDEF"] != 550 || stats(p.Self)["ENHANCED_MDEF"] != 550 {
		t.Fatalf("blessing of the onyx %+v", p.Self)
	}
	if p = cast(t, 22181003, 20); p.Party == nil || stats(p.Party)["SOUL_STONE"] != 1 || p.Cooldown != 360*time.Second || p.MPCost != 100 {
		t.Fatalf("soul stone %+v", p)
	}
	if p = cast(t, 22181004, 20); stats(p.Self)["ONYX_WILL"] != 100 || stats(p.Self)["STANCE"] != 90 {
		t.Fatalf("onyx will %+v", p.Self)
	}
	if p = cast(t, 22161005, 10); !p.Toggle || p.MPCost != 20 || p.Self.Duration != skills.Permanent || stats(p.Self)["TELEPORT_MASTERY"] != 20 {
		t.Fatalf("teleport mastery %+v", p)
	}
	if p = cast(t, 22111001, 20); stats(p.Self)["MAGIC_GUARD"] != 85 {
		t.Fatalf("magic guard %+v", p.Self)
	}
	h, _ := skills.Lookup(22161003)
	if p = cast(t, 22161003, 5); p.Self != nil || p.Party != nil || p.HPCost != 50 || p.MPCost != 50 || p.Cooldown != time.Minute || h.Attack.Field == nil || h.Attack.Field.AreaType != protocol.AreaRecovery || !h.Attack.Field.RecoverMP || h.Attack.Field.Interval != 2500*time.Millisecond || !h.Attack.Field.Box.Set {
		t.Fatalf("recovery aura %+v %+v", p, h.Attack)
	}
	if b, _ := skills.Lookup(22141004); b.Cast == nil || b.Attack == nil || !b.Attack.TeleportKey || b.Attack.Kind != 0 {
		t.Fatalf("dragon blink %+v", b.Attack)
	}
}

func aran(job int, sk ...domain.SkillState) domain.Character {
	return domain.Character{Job: job, Level: 120, HP: 1000, MaxHP: 1000, Skills: sk}
}

func trigger(t *testing.T, ch domain.Character, hits int) *skills.BuffChange {
	t.Helper()
	d, v := def(t, 21000000, 10)
	var change *skills.BuffChange
	for i := 0; i < hits; i++ {
		var rows []domain.SkillBuff
		for _, b := range ch.SkillBuffs {
			if b.ID == 21000000 && b.ExpiresAt.After(now) {
				rows = append(rows, b)
			}
		}
		change = comboCounter(&skills.TriggerContext{Skill: d, Level: 10, Values: v, Character: ch, Rows: rows, Now: now})
		if change == nil {
			return nil
		}
		ch.SkillBuffs = []domain.SkillBuff{{ID: 21000000, Level: 10, Stat: "ARAN_COMBO", Value: change.Buff.Stats[0].Value, Counter: change.Buff.Counter, ExpiresAt: now.Add(skills.Permanent)}}
	}
	return change
}

func comboOf(t *testing.T, c *skills.BuffChange) (combo, value int) {
	t.Helper()
	if c == nil || c.Buff == nil || len(c.Buff.Stats) != 1 || c.Buff.Stats[0].Stat != "ARAN_COMBO" || c.Buff.Duration != skills.Permanent {
		t.Fatalf("combo change %+v", c)
	}
	return c.Buff.Counter, c.Buff.Stats[0].Value
}

// The combo grows by one per client 0xEA, holds its ability value in tens up to
// ten times the level, and restarts after 7 idle seconds.
func TestAranComboCounter(t *testing.T) {
	ch := aran(2112, domain.SkillState{ID: 21000000, Level: 10})
	if combo, v := comboOf(t, trigger(t, ch, 3)); combo != 3 || v != 0 {
		t.Fatalf("first hits %d/%d", combo, v)
	}
	// Last hit 2s ago (expiry = last + Permanent).
	recent := domain.SkillBuff{ID: 21000000, Level: 10, Stat: "ARAN_COMBO", Value: 0, Counter: 9, ExpiresAt: now.Add(-2 * time.Second).Add(skills.Permanent)}
	ch.SkillBuffs = []domain.SkillBuff{recent}
	if combo, v := comboOf(t, trigger(t, ch, 2)); combo != 11 || v != 10 {
		t.Fatalf("tenth combo %d/%d", combo, v)
	}
	recent.Counter = 250
	ch.SkillBuffs = []domain.SkillBuff{recent}
	if combo, v := comboOf(t, trigger(t, ch, 1)); combo != 251 || v != 100 {
		t.Fatalf("capped ability %d/%d", combo, v)
	}
	if got := comboBuffValue(55, 3); got != 30 {
		t.Fatalf("level caps the ability: %d", got)
	}
	idle := recent
	idle.ExpiresAt = now.Add(-8 * time.Second).Add(skills.Permanent)
	ch.SkillBuffs = []domain.SkillBuff{idle}
	if combo, v := comboOf(t, trigger(t, ch, 1)); combo != 1 || v != 0 {
		t.Fatalf("idle reset %d/%d", combo, v)
	}
	if trigger(t, aran(122, domain.SkillState{ID: 21000000, Level: 10}), 3) != nil {
		t.Fatal("non-Aran gained combo")
	}
}

// Combo Recharge adds y combos to the live combo with the cast.
func TestComboRechargeChangesTheComboRow(t *testing.T) {
	_, v := def(t, 21111009, 20)
	ch := aran(2111, domain.SkillState{ID: 21000000, Level: 10}, domain.SkillState{ID: 21111009, Level: 20})
	ch.MaxHP = 1000
	ch.SkillBuffs = []domain.SkillBuff{{ID: 21000000, Level: 10, Stat: "ARAN_COMBO", Counter: 40, Value: 40, ExpiresAt: now.Add(-5 * time.Second).Add(skills.Permanent)}}
	p, err := castAs(t, 21111009, 20, ch)
	if err != nil || len(p.Changes) != 1 || p.Changes[0].SkillID != 21000000 {
		t.Fatalf("recharge %+v %v", p, err)
	}
	if combo, value := comboOf(t, &p.Changes[0].Change); combo != 40+v["y"] || value != comboBuffValue(40+v["y"], 10) {
		t.Fatalf("recharged combo %d/%d (y %d)", combo, value, v["y"])
	}
	// An idle combo is gone before the recharge adds to it.
	ch.SkillBuffs[0].ExpiresAt = now.Add(-9 * time.Second).Add(skills.Permanent)
	if p, _ = castAs(t, 21111009, 20, ch); p.Changes[0].Change.Buff.Counter != v["y"] {
		t.Fatalf("idle recharge %+v", p.Changes[0].Change.Buff)
	}
}

func TestComboPacketAndLinkedLevels(t *testing.T) {
	if got := comboPacket(0x1234); len(got) != 6 || got[0] != 0x6a || got[1] != 0x01 || got[2] != 0x34 || got[3] != 0x12 {
		t.Fatalf("combo packet % x", got)
	}
	if h, _ := skills.Lookup(21000000); h.CounterPacket == nil || h.OnClientCombo == nil || h.OnAttack != nil {
		t.Fatal("combo ability must count client 0xEA and answer with 0x153, not count attacks")
	}
	for id, parent := range map[int]int{21110007: 21110002, 21110008: 21110002, 21120009: 21120002, 21120010: 21120002} {
		if s := spec(t, id); s.LevelFrom != parent || s.Kind != combat.Melee {
			t.Errorf("%d level from %d", id, s.LevelFrom)
		}
	}
}

func TestSoulStoneRevives(t *testing.T) {
	d, v := def(t, 22181003, 20)
	h, _ := skills.Lookup(22181003)
	c := &skills.TriggerContext{Skill: d, Level: 20, Values: v, Now: now}
	if h.OnLethal(c) != nil {
		t.Fatal("revived without the stone")
	}
	_, v10 := def(t, 22181003, 10)
	c.Rows = []domain.SkillBuff{row(22181003, 10, "SOUL_STONE", 1, time.Minute)}
	if got := h.OnLethal(c); got == nil || !got.Cancel || !got.ReviveAtSpawn || got.ReviveHPPercent != v10["x"] || v10["x"] != 50 {
		t.Fatalf("soul stone %+v", got)
	}
}

func passiveBonus(t *testing.T, id, level int, ch domain.Character) skills.Bonus {
	t.Helper()
	h, _ := skills.Lookup(id)
	if h.Passive == nil {
		t.Fatalf("%d has no passive", id)
	}
	d, v := def(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: d, Level: level, Values: v, Character: ch, Now: now})
}

func TestAranPassives(t *testing.T) {
	ch := aran(2112)
	ch.SkillBuffs = []domain.SkillBuff{row(21000000, 10, "ARAN_COMBO", 70, time.Hour)}
	if b := passiveBonus(t, 21000000, 10, ch); b.WATK != 7 {
		t.Fatalf("combo ability %+v", b)
	}
	if b := passiveBonus(t, 21100000, 20, ch); b.Mastery != 0 {
		t.Fatalf("mastery without a polearm %+v", b)
	}
	ch.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1442000}}
	if b := passiveBonus(t, 21100000, 20, ch); b.Mastery != 50 || b.ACC != 120 {
		t.Fatalf("polearm mastery %+v", b)
	}
	high := ch
	high.Job = 2112
	high.Skills = append(append([]domain.SkillState(nil), ch.Skills...), domain.SkillState{ID: 21120001, Level: 20})
	if b := passiveBonus(t, 21100000, 20, high); b.Mastery != 0 || b.ACC != 120 {
		t.Fatalf("polearm mastery with High Mastery %+v", b)
	}
	if b := passiveBonus(t, 21101006, 10, ch); b.DamagePercent != 35 {
		t.Fatalf("snow charge damR %+v", b)
	}
	if b := passiveBonus(t, 21110000, 20, ch); b.Critical != 20 || b.CriticalDamage != 20 {
		t.Fatalf("v148 combo critical cr/criticaldamageMax %+v", b)
	}
	// 45 live combo: min(x 10, 45/10) × y 3 = 12 more critical rate.
	combo := ch
	combo.SkillBuffs = []domain.SkillBuff{{ID: 21000000, Stat: "ARAN_COMBO", Value: 40, Counter: 45, ExpiresAt: now.Add(skills.Permanent)}}
	if b := passiveBonus(t, 21110000, 20, combo); b.Critical != 32 || b.CriticalDamage != 20 {
		t.Fatalf("v148 combo critical with 45 combo %+v", b)
	}
	if b := passiveBonus(t, 21110000, 20, aran(2110)); b.Critical != 0 {
		t.Fatalf("combo critical before 2111 %+v", b)
	}
	if b := passiveBonus(t, 21120001, 20, ch); b.WATK != 30 || b.Mastery != 70 || b.CriticalDamageMin != 15 {
		t.Fatalf("high mastery %+v", b)
	}
	if b := passiveBonus(t, 21120012, 30, ch); b.WATK != 30 || b.ACCPercent != 10 {
		t.Fatalf("advanced final attack %+v", b)
	}
	if b := passiveBonus(t, 21110010, 10, ch); b.IgnoreDefense != 40 || b.BossDamagePercent != 20 {
		t.Fatalf("21110010 %+v", b)
	}
}

func hasStatus(e combat.Effect, s combat.MobStat) (combat.MobStatusApply, bool) {
	for _, a := range e.MonsterStatus {
		if a.Stat == s {
			return a, true
		}
	}
	return combat.MobStatusApply{}, false
}

// Snow Charge slows and Body Pressure neutralises on Java's attack list only.
func TestAranBuffStatusesOnHit(t *testing.T) {
	ch := aran(2112, domain.SkillState{ID: 21101006, Level: 10}, domain.SkillState{ID: 21101003, Level: 10})
	ch.SkillBuffs = []domain.SkillBuff{row(21101006, 10, "WK_CHARGE", -40, time.Minute), row(21101003, 10, "BODY_PRESSURE", 5, time.Minute)}
	ctx := prepared(t, 21000002, 20, ch)
	slow, ok := hasStatus(ctx.Effect, combat.MobSpeed)
	if !ok || slow.Value != -40 || slow.Duration != 20*time.Second || slow.Prop != 100 {
		t.Fatalf("snow charge slow %+v", ctx.Effect.MonsterStatus)
	}
	if bp, ok := hasStatus(ctx.Effect, combat.MobBodyPressure); !ok || bp.Duration != 5*time.Second || bp.Prop != 40 {
		t.Fatalf("body pressure %+v", ctx.Effect.MonsterStatus)
	}
	if ctx = prepared(t, 21100007, 20, ch); len(ctx.Effect.MonsterStatus) != 0 {
		t.Fatalf("combo fenrir is not on Java's list %+v", ctx.Effect.MonsterStatus)
	}
	if ctx = prepared(t, 21000002, 20, aran(2112)); len(ctx.Effect.MonsterStatus) != 0 {
		t.Fatalf("no buffs, no statuses %+v", ctx.Effect.MonsterStatus)
	}
}

func TestAranAttackRules(t *testing.T) {
	for _, id := range []int{21000004, 21100007, 21120006} {
		if s := spec(t, id); s.Kind != combat.Shoot || s.Ammo != combat.AmmoNone {
			t.Errorf("%d must be a ranged attack without ammo", id)
		}
	}
	for _, id := range []int{21110011, 21000002, 21100001, 21100002, 21100010, 21110003, 21110006, 21110007, 21110008, 21120005, 21120009, 21120010, 21120012} {
		if spec(t, id).Kind != combat.Melee {
			t.Errorf("%d must be melee", id)
		}
	}
	_, w := def(t, 21110002, 20)
	ch := aran(2111, domain.SkillState{ID: 21110002, Level: 20})
	if ctx := prepared(t, 21000004, 10, ch); ctx.Effect.Damage != 480+w["w"] {
		t.Fatalf("combo smash with full swing %d", ctx.Effect.Damage)
	}
	ch = aran(2112, domain.SkillState{ID: 21120011, Level: 10})
	if ctx := prepared(t, 21100002, 10, ch); ctx.Effect.Damage != 230+60 {
		t.Fatalf("final charge with fast move %d", ctx.Effect.Damage)
	}
	if ctx := prepared(t, 21110006, 20, aran(2111)); len(ctx.Effect.MonsterStatus) != 0 || len(spec(t, 21110006).OnHit) != 1 {
		t.Fatalf("rolling spin stun %+v", spec(t, 21110006).OnHit)
	}
	if ctx := prepared(t, 21110011, 20, aran(2111)); len(ctx.Effect.MonsterStatus) != 1 || !ctx.Effect.MonsterStatus[0].Poison {
		t.Fatalf("combo judgment DoT %+v", ctx.Effect.MonsterStatus)
	}
	ctx := prepared(t, 21120006, 30, aran(2112))
	if fr, ok := hasStatus(ctx.Effect, combat.MobFreeze); !ok || fr.Duration != 80*time.Second {
		t.Fatalf("tempest freeze %+v", ctx.Effect.MonsterStatus)
	}
	md := spec(t, 21120006).MaxDamage
	if md(combat.DamageContext{HP: 12345}) != 12345 || md(combat.DamageContext{Mob: content.Mob{Boss: true}, HP: 9_000_000}) != 500000 {
		t.Fatal("tempest ceiling")
	}
	// Body Pressure's body attack needs the buff.
	s := spec(t, 21101003)
	if s.Kind != combat.Body || s.Prepare(&combat.AttackContext{Character: aran(2110), Now: now}) == nil {
		t.Fatal("body attack accepted without body pressure")
	}
	on := aran(2110)
	on.SkillBuffs = []domain.SkillBuff{row(21101003, 10, "BODY_PRESSURE", 5, time.Minute)}
	if err := s.Prepare(&combat.AttackContext{Character: on, Now: now}); err != nil {
		t.Fatal(err)
	}
}

func TestComboDrainHealsFromDamage(t *testing.T) {
	ch := aran(2110)
	if hp, _ := comboDrain(&combat.AttackContext{Character: ch, Now: now}, 10000); hp != 0 {
		t.Fatal("drain without the buff")
	}
	ch.SkillBuffs = []domain.SkillBuff{row(21100005, 10, "COMBO_DRAIN", 10, time.Minute)}
	if hp, _ := comboDrain(&combat.AttackContext{Character: ch, Now: now}, 3000); hp != 300 {
		t.Fatalf("drain %d", hp)
	}
	if hp, _ := comboDrain(&combat.AttackContext{Character: ch, Now: now}, 100000); hp != 500 {
		t.Fatalf("drain capped at half MaxHP: %d", hp)
	}
}

func evan(job int, sk ...domain.SkillState) domain.Character {
	return domain.Character{Job: job, Level: 120, Skills: sk}
}

func TestEvanAttacks(t *testing.T) {
	if ctx := prepared(t, 22101000, 20, evan(2210)); ctx.Effect.Element != combat.Fire || ctx.Kind != combat.Magic {
		t.Fatalf("fire circle %+v", ctx.Effect)
	}
	reset := evan(2213)
	reset.SkillBuffs = []domain.SkillBuff{row(22131002, 10, "ELEMENT_RESET", 100, time.Minute)}
	if ctx := prepared(t, 22101000, 20, reset); ctx.Effect.Element != combat.Physical {
		t.Fatal("elemental reset keeps the element")
	}
	if ctx := prepared(t, 22121000, 20, evan(2212)); ctx.Effect.Element != combat.Ice {
		t.Fatal("ice breath element")
	} else if fr, ok := hasStatus(ctx.Effect, combat.MobFreeze); !ok || fr.Duration != 10*time.Second {
		t.Fatalf("ice breath freeze %+v", ctx.Effect.MonsterStatus)
	}
	for _, id := range []int{22131000, 22141001, 22151001, 22181001} {
		if _, ok := hasStatus(prepared(t, id, 10, evan(2218)).Effect, combat.MobStun); !ok {
			t.Errorf("%d must stun", id)
		}
	}
	for _, id := range []int{22151002, 22171003} {
		if p, ok := hasStatus(prepared(t, id, 10, evan(2218)).Effect, combat.MobPoison); !ok || !p.Poison {
			t.Errorf("%d must DoT", id)
		}
	}
	ctx := prepared(t, 22161002, 10, evan(2216))
	if im, ok := hasStatus(ctx.Effect, combat.MobWeakness); !ok || im.ValueFrom != "x" || ctx.Effect.Element != combat.Dark {
		t.Fatalf("phantom imprint %+v", ctx.Effect)
	}
	st := combat.MobStatuses{combat.MobWeakness: &combat.ActiveStatus{Stat: combat.MobWeakness, Value: 20, SkillID: 22161002}}
	if got := imprintCeiling(combat.DamageContext{Statuses: st}, 1000); got != 1200 {
		t.Fatalf("imprint ceiling %d", got)
	}
	if got := imprintCeiling(combat.DamageContext{}, 1000); got != 1000 {
		t.Fatalf("no imprint %d", got)
	}
}

func TestKillerWingsMarksFirstTarget(t *testing.T) {
	d, v := def(t, 22151002, 10)
	c := &skills.TriggerContext{Skill: d, Level: 10, Values: v, Attack: &combat.AttackEvent{SkillID: 22151002, Targets: []int32{77, 78}}, Now: now}
	if got := killerWings(c); got == nil || stats(got.Buff)["HOMING_BEACON"] != 77 || got.Buff.Duration != skills.Permanent {
		t.Fatalf("killer wings %+v", got)
	}
	c.Attack.SkillID = 22001001
	if killerWings(c) != nil {
		t.Fatal("another attack marked")
	}
}

func TestEvanPassives(t *testing.T) {
	if b := passiveBonus(t, 22000000, 20, evan(2200)); b.MATK != 20 {
		t.Fatalf("dragon soul %+v", b)
	}
	if b := passiveBonus(t, 22000000, 20, evan(2001)); b.MATK != 0 {
		t.Fatalf("dragon soul before 2200 %+v", b)
	}
	if b := passiveBonus(t, 22120002, 10, evan(2213)); b.Mastery != 0 {
		t.Fatalf("spell mastery without wand %+v", b)
	}
	wand := evan(2213)
	wand.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1372000}}
	if b := passiveBonus(t, 22120002, 10, wand); b.Mastery != 50 || b.MATK != 10 || b.Critical != 10 {
		t.Fatalf("spell mastery %+v", b)
	}
	// v148 0x6D9733: Magic Mastery's mastery replaces Spell Mastery's.
	wand.Skills = append(wand.Skills, domain.SkillState{ID: 22170001, Level: 30})
	wand.Job = 2217
	if b := passiveBonus(t, 22120002, 10, wand); b.Mastery != 0 || b.MATK != 10 || b.Critical != 10 {
		t.Fatalf("spell mastery with magic mastery %+v", b)
	}
	if b := passiveBonus(t, 22140000, 15, evan(2214)); b.Critical != 30 || b.CriticalDamageMin != 15 {
		t.Fatalf("critical magic %+v", b)
	}
	if b := passiveBonus(t, 22140000, 15, evan(2213)); b.Critical != 0 {
		t.Fatalf("critical magic before 2214 %+v", b)
	}
	if b := passiveBonus(t, 22150000, 15, evan(2215)); b.MPConsume != 50 || b.ElementAmp != 50 {
		t.Fatalf("magic amplification %+v", b)
	}
	if b := passiveBonus(t, 22160000, 5, evan(2216)); b.DamagePercent != 35 {
		t.Fatalf("dragon fury %+v", b)
	}
	if b := passiveBonus(t, 22170001, 30, evan(2217)); b.MATK != 30 || b.Mastery != 70 || b.CriticalDamageMin != 15 {
		t.Fatalf("magic mastery %+v", b)
	}
}
