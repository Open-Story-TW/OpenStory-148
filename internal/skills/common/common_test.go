package common_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/common"
)

// beginnerIDs expands a suffix over every beginner family.
func beginnerIDs(suffixes ...int) []int {
	var out []int
	for _, f := range []int{0, 1000, 2000, 2001, 2002, 2003, 3000, 3001} {
		for _, s := range suffixes {
			out = append(out, f*10000+s)
		}
	}
	return out
}

// pending are thread Z skills waiting for something outside this package;
// Open-Maple docs/server/skills/Z.md explains each group. IDs that are not enabled in the WZ
// are ignored.
var pendingGroups = map[string][]int{
	"潛能開放: Java HIDDEN_POTENTIAL 1 without a duration, use unknown": beginnerIDs(93),
	"冰衝擊: type 32 without damage, needs MCP observation":            beginnerIDs(104),
	"法老的憤怒: Pyramid PQ (Java DamageParse)":                          beginnerIDs(1020),
	"unnamed beginner attacks with no Java handling":                beginnerIDs(1092, 1094, 1095),
	"助手 (x013): no Java handling":                                   beginnerIDs(1013),
	"捕獲／獵殺殭屍: monster capture system":                               append(beginnerIDs(1061), 80001132),
	"Mercedes morph skill, use unknown":                             {20020111},
	"Legacy removes these on job advance":                           {20000014, 20000015, 20000016, 20021166, 20021181, 30010166, 30011167, 30011168, 30011169, 30011170},
	"Aran tutorial passives, use unknown":                           {20000017, 20000018},
	"Phantom hidden skills with no Java handling":                   {20031166, 20031181},
	"Phantom skill steal (MP thread)":                               {20031207, 20031208},
	"flying mounts":                                                 {20010022, 30011109, 30011159},
	"神龍擺尾: Legacy custom pet summon":                                {80001130},
	"測謊機 lie detector":                                              {8001000, 9001009},
	"復活: no lt/rb, Legacy resurrects nobody":                        {9001005},
	"monster-status skills 9000xxxx, entry point unknown":           {90000000, 90001001, 90001002, 90001003, 90001004, 90001005, 90001006},
	"guild skills: guild system (content/guild_skills.go)":          {91000000, 91000001, 91000002, 91000003, 91000004, 91000005, 91000006},
	"professions: crafting system (deferred by the plan)":           {92000000, 92010000, 92020000, 92030000, 92040000},
}

func pending() map[int]string {
	out := map[int]string{}
	for reason, ids := range pendingGroups {
		for _, id := range ids {
			out[id] = reason
		}
	}
	return out
}

// Every enabled thread Z skill of the plan inventory is registered or pending.
func TestEveryCommonSkillIsRegisteredOrPending(t *testing.T) {
	raw, err := os.ReadFile("../testdata/skill-plan-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var inv struct {
		Skills []struct {
			ID         int    `json:"id"`
			Thread     string `json:"thread"`
			WZDisabled bool   `json:"wz_disabled"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	wait := pending()
	seen, registered := 0, 0
	for _, s := range inv.Skills {
		if s.Thread != "Z" || s.WZDisabled {
			continue
		}
		seen++
		_, ok := skills.Lookup(s.ID)
		_, p := wait[s.ID]
		if ok == p {
			t.Errorf("skill %d: registered=%v pending=%v", s.ID, ok, p)
		}
		if ok {
			registered++
		}
	}
	if seen != 376 {
		t.Fatalf("thread Z has %d enabled skills, want 376", seen)
	}
	t.Logf("registered %d/%d", registered, seen)
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
	return h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: c, Now: time.Unix(0, 0)})
}

func mustCast(t *testing.T, id, level int, c domain.Character) skills.CastPlan {
	t.Helper()
	p, err := cast(t, id, level, c)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	if b != nil {
		for _, s := range b.Stats {
			out[s.Stat] = s.Value
		}
	}
	return out
}

func passive(t *testing.T, id int, c domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("skill %d has no passive", id)
	}
	def, v := values(t, id, 1)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: 1, Values: v, Character: c})
}

// Every registered buff stat has an audited native layout.
func TestBuffStatsAreSendable(t *testing.T) {
	gm := domain.Character{GM: 1, Equipment: []domain.Equipment{{ItemID: 1902000, Slot: -18}, {ItemID: 1912000, Slot: -19}}}
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "common" {
			continue
		}
		h, _ := skills.Lookup(id)
		if h.Cast == nil || id%10000 == 1026 { // Soaring needs a flyable map.
			continue
		}
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatal(err)
		}
		character := gm
		if skills.DojoSkill(id) {
			character.MapID = content.DojoFloorMap(1, 0)
		}
		if id == 80001065 || id == 80001089 {
			character.MapID = content.EscapeTower
			character.SkillBuffs = []domain.SkillBuff{{ID: 80001065, Stat: "MONSTER_RIDING", Value: 1992002, ExpiresAt: time.Unix(1, 0)}}
		}
		p, err := cast(t, id, def.MaxLevel, character)
		if err != nil {
			t.Errorf("skill %d: %v", id, err)
			continue
		}
		for _, b := range []*skills.Buff{p.Self, p.Party} {
			if b == nil {
				continue
			}
			names := make([]buffstat.Stat, 0, len(b.Stats))
			for _, s := range b.Stats {
				names = append(names, s.Stat)
			}
			if _, err := buffstat.Resolve(names); err != nil {
				t.Errorf("skill %d: %v", id, err)
			}
		}
	}
}

func TestBeginnerFamilies(t *testing.T) {
	for _, id := range []int{1000, 10001000, 20001000, 20011000, 20021000, 20031000, 30001000, 30011000} {
		if h, ok := skills.Lookup(id); !ok || !h.Attack.Allows(combat.Magic) {
			t.Errorf("three snails %d", id)
		}
	}
	p := mustCast(t, 10001001, 1, domain.Character{})
	if s := stats(p.Self); s["RECOVERY"] != 4 || p.Self.Tick != 5*time.Second || p.Cooldown != 120*time.Second {
		t.Fatalf("cygnus recovery %+v", p.Self)
	}
	p = mustCast(t, 30001001, 3, domain.Character{})
	if s := stats(p.Self); s["INFILTRATE"] != 1 || s["SPEED"] != 15 || s["RECOVERY"] != 0 || p.Self.Duration != 30*time.Second {
		t.Fatalf("resistance infiltrate %+v", p.Self)
	}
	p = mustCast(t, 20021001, 2, domain.Character{})
	if s := stats(p.Self); s["RECOVERY"] != 1 || s["SPEED"] != 10 {
		t.Fatalf("mercedes 1001 follows Java (RECOVERY): %+v", p.Self)
	}
	p = mustCast(t, 1005, 1, domain.Character{})
	if s := stats(p.Self); s["ECHO_OF_HERO"] != 4 || p.Party != nil || p.MPCost != 30 || p.Cooldown != 7200*time.Second || p.Self.Duration != 2400*time.Second {
		t.Fatalf("echo %+v %+v", p, p.Self)
	}
	if p := mustCast(t, 10001075, 1, domain.Character{}); stats(p.Self)["ECHO_OF_HERO"] != 4 {
		t.Fatal("empress's prayer")
	}
}

func TestDojoEnergySkillsOnlyOnDojangFloors(t *testing.T) {
	floor := domain.Character{MapID: 925020100}
	for _, id := range []int{1010, 10001010, 20011010, 30011010, 50001010} {
		if _, err := cast(t, id, 1, domain.Character{GM: 1, MapID: 100000000}); err == nil {
			t.Fatalf("%d accepted outside the dojo", id)
		}
		if _, err := cast(t, id, 1, domain.Character{MapID: 925020600}); err == nil {
			t.Fatalf("%d accepted on rest floor 6", id)
		}
		if p := mustCast(t, id, 1, floor); stats(p.Self)["DIVINE_BODY"] != 1 || p.Self.Duration != 30*time.Second {
			t.Fatalf("%d %+v", id, p.Self)
		}
	}
	for _, id := range []int{1011, 20001011, 50001011} {
		if _, err := cast(t, id, 1, domain.Character{MapID: 925020002}); err == nil {
			t.Fatalf("%d accepted in the exit room", id)
		}
		if p := mustCast(t, id, 1, domain.Character{MapID: 925024709}); stats(p.Self)["BERSERK_FURY"] != 200 || p.Self.Duration != 30*time.Second {
			t.Fatalf("%d %+v", id, p.Self)
		}
	}
}

func TestBambooRainIsTheClientLineValue(t *testing.T) {
	for _, id := range []int{1009, 10001009, 20001009, 20011009, 20021009, 20031009, 30001009, 30011009, 50001009} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Melee) || h.Attack.Allows(combat.Magic) || h.Attack.MaxDamage == nil {
			t.Fatalf("%d attack %+v", id, h.Attack)
		}
		ctx := &combat.AttackContext{Character: domain.Character{MapID: 925020100}, Effect: combat.Effect{SkillID: id}}
		if err := h.Attack.Prepare(ctx); err != nil {
			t.Fatalf("%d on floor 1: %v", id, err)
		}
		ctx.Character.MapID = 925020600
		if err := h.Attack.Prepare(ctx); err == nil {
			t.Fatalf("%d accepted on a rest floor", id)
		}
	}
	h, _ := skills.Lookup(1009)
	for _, c := range []struct {
		hp   int64
		boss bool
		want int
	}{{52500, true, 15750}, {23500000, true, 7050000}, {7, true, 2}, {1234, false, 1234}} {
		if got := h.Attack.MaxDamage(combat.DamageContext{Mob: content.Mob{HP: c.hp, Boss: c.boss}}); got != c.want {
			t.Fatalf("hp %d boss %v: %d, want %d", c.hp, c.boss, got, c.want)
		}
	}
}

func TestAngelsAndBlessings(t *testing.T) {
	for id, v := range map[int]int{1085: 5, 10001087: 10, 20011090: 5, 30011179: 12} {
		h, _ := skills.Lookup(id)
		if h.Attack == nil || h.Attack.Summon == nil || h.Attack.Summon.MoveAbility != 1 || h.Attack.Summon.Duration != skills.Permanent || h.Attack.Allows(combat.Summoned) {
			t.Errorf("angel %d summon %+v", id, h.Attack)
		}
		p := mustCast(t, id, 1, domain.Character{})
		if s := stats(p.Self); s["ANGEL_ATK"] != v || s["ANGEL_MATK"] != v || p.Self.Duration != skills.Permanent {
			t.Errorf("angel %d %+v", id, p.Self)
		}
	}
	p := mustCast(t, 30010180, 1, domain.Character{})
	if s := stats(p.Self); s["ANGEL_ATK"] != 12 || s["ANGEL_MATK"] != 12 || p.Self.Duration != 600*time.Second {
		t.Fatalf("blessing %+v", p.Self)
	}
}

func TestV148AngelSummonsUseEquippedRingInsteadOfLearnedRank(t *testing.T) {
	now := time.Unix(100, 0)
	for skill, ring := range map[int]int{20001085: 1112585, 10001087: 1112586, 20001090: 1112594, 30011179: 1112663} {
		actor := domain.Character{Equipment: []domain.Equipment{{ItemID: ring, Slot: -12}}}
		if level, ok := skills.CastLevel(actor, skill, skill, 1, now); !ok || level != 1 {
			t.Errorf("skill %d with its equipped ring: level %d, accepted %t", skill, level, ok)
		}
		if _, ok := skills.CastLevel(actor, skill, skill, 2, now); ok {
			t.Errorf("skill %d accepted a fabricated higher rank", skill)
		}
		actor.Skills = []domain.SkillState{{ID: skill, Level: 1}}
		for _, equip := range []domain.Equipment{
			{ItemID: ring, Slot: 1}, {ItemID: ring, Slot: -112}, {ItemID: 1112000, Slot: -12},
			{ItemID: ring, Slot: -12, ExpiresAt: now.UnixMilli()},
		} {
			actor.Equipment[0] = equip
			if _, ok := skills.CastLevel(actor, skill, skill, 1, now); ok {
				t.Errorf("skill %d accepted learned rank without its valid equipped ring: %+v", skill, equip)
			}
		}
	}
}

func TestV148EmpressCallUsesBothWZVitalPercentages(t *testing.T) {
	for _, id := range []int{10000074, 50000074} {
		h, ok := skills.Lookup(id)
		if !ok || h.Passive == nil || h.Kind != skills.KindPassive {
			t.Fatalf("skill %d missing permanent v148 vital effect", id)
		}
		b := h.Passive(&skills.PassiveContext{Values: map[string]int{"x": 20}})
		if b.MaxHPPercent != 20 || b.MaxMPPercent != 20 {
			t.Errorf("skill %d v148 x=20 effect: %+v", id, b)
		}
	}
}

func TestIceKnight(t *testing.T) {
	p := mustCast(t, 20031105, 1, domain.Character{})
	if s := stats(p.Self); s["ICE_SKILL"] != 1 || s["MORPH"] != 2003 || p.Self.Duration != skills.Permanent {
		t.Fatalf("ice knight %+v", p.Self)
	}
	h, _ := skills.Lookup(99)
	ctx := &combat.AttackContext{Effect: combat.Effect{SkillID: 99, Time: 3 * time.Second}, Now: time.Unix(10, 0)}
	if h.Attack.Prepare(ctx) == nil {
		t.Fatal("ice attack without the morph")
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{{ID: 1105, Stat: "ICE_SKILL", Value: 1, ExpiresAt: time.Unix(100, 0)}}
	if err := h.Attack.Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0].Duration != 6*time.Second {
		t.Fatalf("ice attack %v %+v", err, ctx.Effect.MonsterStatus)
	}
}

func TestMounts(t *testing.T) {
	if _, err := cast(t, 10001017, 1, domain.Character{}); err == nil {
		t.Fatal("low level mount without an equipped mount")
	}
	p := mustCast(t, 10001017, 1, domain.Character{Equipment: []domain.Equipment{{ItemID: 1902000, Slot: -18}, {ItemID: 1912000, Slot: -19}}})
	if s := stats(p.Self); s["MONSTER_RIDING"] != 1902000 || !p.Toggle || p.Self.Duration != skills.Permanent {
		t.Fatalf("low level mount %+v", p.Self)
	}
	if _, err := cast(t, 80001000, 1, domain.Character{Equipment: []domain.Equipment{{ItemID: 1902000, Slot: -18}}}); err == nil {
		t.Fatal("monster riding without a saddle")
	}
	p = mustCast(t, 80001000, 1, domain.Character{Equipment: []domain.Equipment{{ItemID: 1902000, Slot: -18}, {ItemID: 1912000, Slot: -19}}})
	if stats(p.Self)["MONSTER_RIDING"] != 1902000 {
		t.Fatalf("monster riding %+v", p.Self)
	}
}

func TestGMSkills(t *testing.T) {
	gm := domain.Character{GM: 1}
	for _, id := range []int{9001000, 9001001, 9001002} {
		if _, err := cast(t, id, 1, domain.Character{}); err == nil {
			t.Errorf("non-GM %d accepted", id)
		}
	}
	if p := mustCast(t, 9001000, 1, gm); !p.Dispel || p.Self != nil {
		t.Fatalf("gm dispel %+v", p)
	}
	if s := stats(mustCast(t, 9001001, 1, gm).Self); s["SPEED"] != 40 || s["JUMP"] != 20 {
		t.Fatalf("gm haste %v", s)
	}
	if s := stats(mustCast(t, 9001002, 1, gm).Self); s["HOLY_SYMBOL"] != 50 {
		t.Fatalf("gm holy symbol %v", s)
	}
	if s := stats(mustCast(t, 9001003, 1, gm).Self); s["BLESS"] != 1 || s["WATK"] != 20 || s["WDEF"] != 100 || s["AVOID"] != 100 {
		t.Fatalf("gm bless %v", s)
	}
	if s := stats(mustCast(t, 9001008, 1, gm).Self); s["MAXHP"] != 60 || s["MAXMP"] != 60 {
		t.Fatalf("gm hyper body %v", s)
	}
}

func TestEventBuffs(t *testing.T) {
	p := mustCast(t, 80001036, 1, domain.Character{})
	if s := stats(p.Self); s["VIRTUE_EFFECT"] != 1 || s["ANGEL_ATK"] != 40 || s["ANGEL_SPEED"] != 60 || s["ANGEL_JUMP"] != 23 || p.Self.Duration != 3600*time.Second {
		t.Fatalf("virtue %+v", p.Self)
	}
	if s := stats(mustCast(t, 80001041, 1, domain.Character{}).Self); s["VIRTUE_EFFECT"] != 0 || s["ANGEL_MATK"] != 20 {
		t.Fatalf("green %v", s)
	}
	p = mustCast(t, 1189, 1, domain.Character{})
	if p.Party == nil || p.Heal == nil || p.Area == nil || p.MPCost != 500 || p.Cooldown != 90*time.Second {
		t.Fatalf("victor's blessing %+v", p)
	}
	h, _ := skills.Lookup(80001129)
	if h.Attack.Summon == nil || !h.Attack.Allows(combat.Summoned) {
		t.Fatal("mr dragon summon")
	}
}

func TestPassives(t *testing.T) {
	for _, id := range []int{190, 10000190, 20000190, 20010190, 20020190, 20030190, 30000190, 30010190, 50000190} {
		if def, err := content.SkillDefinition("", id); err == nil && !def.Disabled {
			want := skills.Bonus{STR: 5, DEX: 5, INT: 5, LUK: 5, WATK: 5, MATK: 5}
			if got := passive(t, id, domain.Character{}); got != want {
				t.Errorf("%d got %+v, want %+v", id, got, want)
			}
		}
	}
	for _, id := range []int{110, 80000000} {
		want := skills.Bonus{STR: 15, DEX: 15, INT: 15, LUK: 15, MaxHPPercent: 5, MaxMPPercent: 5}
		if got := passive(t, id, domain.Character{}); got != want {
			t.Errorf("%d got %+v, want %+v", id, got, want)
		}
	}
	for id, want := range map[int]skills.Bonus{
		20030204: {Critical: 10},
		80000002: {Critical: 10},
		80000016: {STR: 7, DEX: 7, INT: 7, LUK: 7, WATK: 7, MATK: 7, IgnoreDefense: 5},
		80000136: {STR: 3, DEX: 3, INT: 3, LUK: 3, WATK: 1, MATK: 1, MaxHP: 400, MaxMP: 400},
		80000133: {STR: 1, DEX: 1, INT: 1, LUK: 1, MaxHP: 100, MaxMP: 100},
		80000134: {STR: 2, DEX: 2, INT: 2, LUK: 2, MaxHP: 200, MaxMP: 200},
		80000135: {STR: 3, DEX: 3, INT: 3, LUK: 3, MaxHP: 300, MaxMP: 300},
		80000137: {STR: 1, DEX: 1, INT: 1, LUK: 1, WDEF: 40, MDEF: 40},
		20000194: {WATK: 10, WDEF: 300, Speed: 10, Critical: 5, BossDamagePercent: 5},
		20020112: {Speed: 10, Jump: 20},
		228:      {Speed: 50, Jump: 20, SpeedLimitBonus: 30},
	} {
		if got := passive(t, id, domain.Character{}); got != want {
			t.Errorf("%d got %+v, want %+v", id, got, want)
		}
	}
	if b := passive(t, 20010194, domain.Character{}); b != (skills.Bonus{STR: 10, DEX: 10, INT: 10, LUK: 10, MATK: 10, MDEF: 300, MaxHPPercent: 15, BossDamagePercent: 5}) {
		t.Fatalf("20010194 %+v", b)
	}
	if b := passive(t, 10000074, domain.Character{}); b.MaxHPPercent != 20 || b.MaxMPPercent != 20 {
		t.Fatalf("10000074 %+v", b)
	}
	if b := passive(t, 30000002, domain.Character{Job: 3200}); b.PotionPercent != 5 {
		t.Fatalf("30000002 %+v", b)
	}
	if b := passive(t, 30000002, domain.Character{Job: 100}); b.PotionPercent != 0 {
		t.Fatalf("30000002 explorer %+v", b)
	}
	if b := passive(t, 20020022, domain.Character{Job: 2300}); b.Critical != 50 {
		t.Fatalf("20020022 %+v", b)
	}
	if b := passive(t, 80000001, domain.Character{}); b.BossDamagePercent != 10 {
		t.Fatalf("80000001 %+v", b)
	}
}

func TestF2eHandlers(t *testing.T) {
	p := mustCast(t, 30011005, 1, domain.Character{})
	if stats(p.Self)["ECHO_OF_HERO"] != 4 || p.MPCost != 10 {
		t.Fatalf("demon echo pays forceCon as MP: %+v", p)
	}
	for _, id := range []int{80001040, 20021110} {
		if p := mustCast(t, id, 1, domain.Character{}); p.MoveTo != 101050000 || p.MPCost != 30 || p.Cooldown != 1800*time.Second || p.Self != nil {
			t.Fatalf("elven blessing %d %+v", id, p)
		}
	}
	h, _ := skills.Lookup(20020109)
	def, v := values(t, 20020109, 1)
	ctx := &skills.TriggerContext{Skill: def, Level: 1, Values: v, Character: domain.Character{Job: 2300, MaxHP: 1000, MaxMP: 400}}
	if c := h.OnTick(ctx); h.TickEvery != 4*time.Second || c == nil || c.HealHP != 50 || c.HealMP != 20 {
		t.Fatalf("elf recovery %+v", c)
	}
	ctx.Character.Job = 100
	if h.OnTick(ctx) != nil {
		t.Fatal("elf recovery for a non-Mercedes")
	}
	h, _ = skills.Lookup(30010111)
	def, v = values(t, 30010111, 1)
	v["prop"] = 100
	ctx = &skills.TriggerContext{Skill: def, Level: 1, Values: v, Character: domain.Character{Job: 3100, MaxHP: 1000}, Attack: &combat.AttackEvent{Damage: 4000}}
	if c := h.OnAttack(ctx); c == nil || c.HealHP != 200 {
		t.Fatalf("death curse %+v", c)
	}
	ctx.Attack.Damage = 100000
	if c := h.OnAttack(ctx); c == nil || c.HealHP != 500 {
		t.Fatalf("death curse cap %+v", c)
	}
}

func TestElvenBlessingEXP(t *testing.T) {
	if b := passive(t, 80001040, domain.Character{Job: 100}); b.EXP != 10 {
		t.Fatalf("80001040 %+v", b.EXP)
	}
	if b := passive(t, 20021110, domain.Character{Job: 2312}); b.EXP != 10 {
		t.Fatalf("20021110 mercedes %+v", b.EXP)
	}
	if b := passive(t, 20021110, domain.Character{Job: 100}); b.EXP != 0 {
		t.Fatalf("20021110 explorer %+v", b.EXP)
	}
}

func TestSoaringNeedsAFlyableMap(t *testing.T) {
	if _, err := cast(t, 10001026, 1, domain.Character{MapID: 10000}); err == nil {
		t.Fatal("soaring on Maple Island")
	}
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("flyable fields load from MAPLEGO_WZ_ROOT")
	}
	if p := mustCast(t, 10001026, 1, domain.Character{MapID: 200110000}); p.MPCost != 90 || !p.Toggle ||
		p.Self == nil || p.Self.Duration != skills.Permanent || stats(p.Self)["SOARING"] != 1 {
		t.Fatalf("soaring %+v", p)
	}
}

func TestGMHide(t *testing.T) {
	if _, err := cast(t, 9001004, 1, domain.Character{}); err == nil {
		t.Fatal("non-GM hide")
	}
	p := mustCast(t, 9001004, 1, domain.Character{GM: 1})
	if stats(p.Self)["GM_HIDE"] != 1 || !p.Toggle || p.Self.Duration != 2*time.Hour {
		t.Fatalf("gm hide %+v", p)
	}
}

func TestEscapeBalloonAndFlightFollowTheNativeFields(t *testing.T) {
	const balloon, flight = 80001065, 80001089
	for _, id := range []int{balloon, flight} {
		def, _ := values(t, id, 1)
		handler, registered := skills.Lookup(id)
		if !def.Disabled || !registered || !handler.AllowDisabledCast || handler.DropOnEnter == nil {
			t.Fatalf("skill %d raw WZ or cast scope changed", id)
		}
		if !handler.DropOnEnter(domain.Character{MapID: content.EscapeApproach}) {
			t.Fatalf("skill %d persisted outside the tower", id)
		}
		if _, err := cast(t, id, 1, domain.Character{MapID: content.EscapeApproach}); err == nil {
			t.Fatalf("skill %d could be activated outside a tower", id)
		}
	}

	for _, field := range []int{content.EscapeTower, content.EscapeTraps} {
		character := domain.Character{MapID: field}
		mount := mustCast(t, balloon, 1, character)
		if !mount.Toggle || mount.MPCost != 10 || stats(mount.Self)["MONSTER_RIDING"] != 1992002 {
			t.Fatalf("field %d had unexpected mount result %+v", field, mount)
		}
		if _, err := cast(t, flight, 1, character); err == nil {
			t.Fatalf("field %d allowed unmounted flight", field)
		}
		character.SkillBuffs = []domain.SkillBuff{{ID: balloon, Stat: "MONSTER_RIDING", Value: 1992002, ExpiresAt: time.Unix(1, 0)}}
		flying := mustCast(t, flight, 1, character)
		if !flying.Toggle || flying.MPCost != 5 || stats(flying.Self)["SOARING"] != 1 {
			t.Fatalf("field %d had unexpected flight result %+v", field, flying)
		}
	}
}

func TestBambooRainPlansTheClientLines(t *testing.T) {
	h, _ := skills.Lookup(1009)
	def, _ := values(t, 1009, 1)
	effect, err := combat.FromSkill(def, 1)
	if err != nil || effect.MobCount != 30 || effect.AttackCount != 1 || !effect.Box.Set {
		t.Fatalf("1009 effect %+v err %v", effect, err)
	}
	boss := content.Mob{ID: 9305100, HP: 52500, Boss: true}
	minion := content.Mob{ID: 9300003, HP: 800}
	attack := protocol.Attack{Opcode: protocol.RecvMeleeAttack, SkillID: 1009, Targets: []protocol.AttackTarget{
		{ObjectID: 1, Lines: []protocol.AttackLine{{Raw: 15750}}},
		{ObjectID: 2, Lines: []protocol.AttackLine{{Raw: 900}}},
	}}
	ctx := combat.AttackContext{
		Kind: combat.Melee, Attack: attack, Effect: effect, Now: time.Unix(100, 0),
		Character: domain.Character{ID: 1, MapID: 925020100, HP: 50},
		Targets:   []combat.TargetInfo{{ObjectID: 1, Mob: boss, X: 300, Y: 7, HP: 52500}, {ObjectID: 2, Mob: minion, X: -200, Y: 7, HP: 800}},
	}
	plan, err := combat.PlanAttack(h.Attack, ctx, nil)
	if err != nil || plan.Targets[0].Lines[0] != 15750 || plan.Targets[1].Lines[0] != 800 || plan.Targets[1].Clipped != 1 || plan.Targets[0].Clipped != 0 {
		t.Fatalf("plan %+v err %v", plan.Targets, err)
	}
	ctx.Character.MapID = 925020002
	if _, err = combat.PlanAttack(h.Attack, ctx, nil); err == nil {
		t.Fatal("planned outside the dojo")
	}
}
