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
// docs/skills/Z.md explains each group. IDs that are not enabled in the WZ
// are ignored.
var pendingGroups = map[string][]int{
	"潛能開放: Java HIDDEN_POTENTIAL 1 without a duration, use unknown":   beginnerIDs(93),
	"冰衝擊: type 32 without damage, needs MCP observation":              beginnerIDs(104),
	"竹竿天擊／法老的憤怒: Mu Lung dojo energy / Pyramid PQ (Java DamageParse)": beginnerIDs(1009, 1020),
	"unnamed beginner attacks with no Java handling":                  beginnerIDs(1092, 1094, 1095),
	"助手 (x013): no Java handling":                                     beginnerIDs(1013),
	"捕獲／獵殺殭屍: monster capture system":                                 append(beginnerIDs(1061), 80001132),
	"水晶花園傳送: WZ x is a map but Java sets no moveTo":                   {20031203},
	"Mercedes/Phantom morph skills, use unknown":                      {20020111, 20031205},
	"Legacy removes these on job advance":                             {20000014, 20000015, 20000016, 20021166, 20021181, 30010166, 30011167, 30011168, 30011169, 30011170},
	"Aran tutorial passives, use unknown":                             {20000017, 20000018},
	"Phantom hidden skills with no Java handling":                     {20031166, 20031181},
	"Phantom skill steal / Judgment Draw (MP thread)":                 {20031207, 20031208, 20031209, 20031210},
	"flying mounts":                                        {20010022, 30011109, 30011159},
	"神龍擺尾: Legacy custom pet summon":                       {80001130},
	"測謊機 lie detector":                                     {8001000, 9001009},
	"復活: no lt/rb, Legacy resurrects nobody":               {9001005},
	"monster-status skills 9000xxxx, entry point unknown":  {90000000, 90001001, 90001002, 90001003, 90001004, 90001005, 90001006},
	"guild skills: guild system (content/guild_skills.go)": {91000000, 91000001, 91000002, 91000003, 91000004, 91000005, 91000006},
	"professions: crafting system (deferred by the plan)":  {92000000, 92010000, 92020000, 92030000, 92040000},
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
		if h.Cast == nil || id%10000 == 1026 { // Soaring needs a flyable map and gives no buff
			continue
		}
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatal(err)
		}
		p, err := cast(t, id, def.MaxLevel, gm)
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

func TestInvincibleBarrierExplorerNeedsGM(t *testing.T) {
	if _, err := cast(t, 1010, 1, domain.Character{}); err == nil {
		t.Fatal("non-GM explorer 1010 accepted")
	}
	if p := mustCast(t, 1010, 1, domain.Character{GM: 1}); stats(p.Self)["DIVINE_BODY"] != 1 || p.Self.Duration != 30*time.Second {
		t.Fatalf("1010 %+v", p.Self)
	}
	if p := mustCast(t, 10001010, 1, domain.Character{}); stats(p.Self)["DIVINE_BODY"] != 1 {
		t.Fatal("cygnus 10001010")
	}
	if p := mustCast(t, 20001011, 1, domain.Character{}); stats(p.Self)["BERSERK_FURY"] != 1 {
		t.Fatal("power explosion")
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
	if b := passive(t, 110, domain.Character{}); b.STR != 15 || b.LUK != 15 || b.MaxHPPercent != 5 || b.MaxMPPercent != 5 {
		t.Fatalf("110 %+v", b)
	}
	if b := passive(t, 80000000, domain.Character{}); b.STR != 15 || b.MaxHPPercent != 0 {
		t.Fatalf("80000000 %+v", b)
	}
	if b := passive(t, 20010194, domain.Character{}); b.MaxHPPercent != 25 {
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
	if p := mustCast(t, 10001026, 1, domain.Character{MapID: 200110000}); p.MPCost != 90 || p.Self != nil {
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
