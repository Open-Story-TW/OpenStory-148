package all

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func resourceCostFixture(t *testing.T) (domain.Character, time.Time) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Item.wz/ItemOption.img.xml":           `<imgdir name="ItemOption.img"><imgdir name="010001"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="mpconReduce" value="33"/><int name="reduceCooltime" value="7"/></imgdir></imgdir></imgdir></imgdir>`,
		"Character.wz/Weapon/01302000.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`,
	}
	for book, data := range map[int]map[int]string{
		211:  {2110001: `<int name="x" value="150"/><int name="y" value="150"/>`},
		212:  {2121003: `<int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="damage" value="100"/>`},
		311:  {3111000: `<int name="x" value="5"/>`},
		232:  {2321005: `<int name="mpConReduce" value="10"/>`},
		3100: {31001001: `<int name="mpCon" value="7"/><int name="forceCon" value="10"/>`, 31001002: `<int name="mpCon" value="7"/>`},
		3212: {32121003: `<int name="mpCon" value="20"/><int name="cooltime" value="40"/><int name="damage" value="100"/>`},
		230:  {2301002: `<int name="mpCon" value="12"/><int name="damage" value="100"/>`},
		3511: {35111004: `<int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="damage" value="100"/><int name="w" value="5"/>`},
		3512: {35121005: `<int name="mpCon" value="7"/><int name="damage" value="100"/><int name="w" value="9"/>`},
		2000: {20000093: `<int name="x" value="100"/>`},
	} {
		var s strings.Builder
		s.WriteString(`<imgdir name="book"><imgdir name="skill">`)
		for id, fields := range data {
			fmt.Fprintf(&s, `<imgdir name="%d"><imgdir name="common"><int name="maxLevel" value="30"/>%s</imgdir></imgdir>`, id, fields)
		}
		s.WriteString(`</imgdir></imgdir>`)
		files[fmt.Sprintf("Skill.wz/%03d.img.xml", book)] = s.String()
	}
	for path, data := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Unix(1800000000, 0)
	c := domain.Character{ID: 1, Job: 212, Level: 150, HP: 100, MP: 100, MaxHP: 100, MaxMP: 100, STR: 4, DEX: 4, INT: 100, LUK: 4,
		Skills:    []domain.SkillState{{ID: 2110001, Level: 1}, {ID: 2121003, Level: 1}},
		Equipment: []domain.Equipment{{ID: 1, Slot: -11, ItemID: 1302000, Potential: domain.EquipmentPotential{Present: true, RawState: 20, Options: [5]uint16{10001}}}}}
	return c, now
}

func TestResourceModifierSourcesPassiveBuffExpiryAndCatalogIsolation(t *testing.T) {
	c, now := resourceCostFixture(t)
	before := c
	c.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	p, err := skills.ResolveResourceCost(c, 2121003, 7, 10*time.Second, now)
	if err != nil || p.MP != 7 || p.Cooldown != 3*time.Second {
		t.Fatal(p, err)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 3111000, Level: 1, Stat: "CONCENTRATE", Value: 5, ExpiresAt: now.Add(time.Second)}, {ID: 2321005, Level: 1, Stat: "HOLY_SHIELD", Value: 99, ExpiresAt: now.Add(time.Second)}}
	// Native CONCENTRATE is its authoritative temporary-stat value;
	// HOLY_SHIELD uses source mpConReduce. floor(7*150/100)-floor(10*48/100)=6.
	p, err = skills.ResolveResourceCost(c, 2121003, 7, 10*time.Second, now)
	if err != nil || p.MP != 6 || p.Cooldown != 3*time.Second {
		t.Fatal(p, err)
	}
	c.Skills[0].ExpiresAt = now
	c.Equipment[0].ExpiresAt = now.UnixMilli()
	p, err = skills.ResolveResourceCost(c, 2121003, 7, 10*time.Second, now.Add(time.Second))
	if err != nil || p.MP != 7 || p.Cooldown != 10*time.Second {
		t.Fatal("expired cost source remained", p, err)
	}
	if before.Equipment[0].Potential.Options[0] != 10001 {
		t.Fatal("source mutated")
	}
	c.Equipment[0].ExpiresAt = 0
	c.Equipment[0].Potential.RawState = 0x15
	if _, err = skills.ResourceModifiers(c, now); err == nil {
		t.Fatal("unknown tier zeroed cost")
	}
}

func TestResourceCastDistinctDemonForceAndNoDoublePaidAttack(t *testing.T) {
	c, now := resourceCostFixture(t)
	c.Job = 3112
	c.Skills = []domain.SkillState{{ID: 31001001, Level: 1}, {ID: 31001002, Level: 1}}
	c.SkillBuffs = []domain.SkillBuff{{Stat: "INFINITY", Value: 1, ExpiresAt: now.Add(time.Second)}}
	p, err := skills.CastResourceCost(c, 31001001, skills.CastPlan{MPCost: 17, Cooldown: 10 * time.Second}, now)
	if err != nil || p.MP != 10 || p.Cooldown != 3*time.Second {
		t.Fatal("force modified by MP percentage/infinity", p, err)
	}
	p, err = skills.CastResourceCost(c, 31001002, skills.CastPlan{MPCost: 7}, now)
	if err != nil || p.MP != 0 {
		t.Fatal("ordinary MP charged Demon", p, err)
	}
	c.SkillBuffs = append(c.SkillBuffs, domain.SkillBuff{Stat: "BOUNDLESS_RAGE", Value: 1, ExpiresAt: now.Add(time.Second)})
	p, err = skills.CastResourceCost(c, 31001001, skills.CastPlan{MPCost: 17}, now)
	if err != nil || p.MP != 0 {
		t.Fatal("boundless force charged", p, err)
	}
	c.Job = 3212
	c.Skills = []domain.SkillState{{ID: 32121003, Level: 1}}
	c.SkillBuffs = []domain.SkillBuff{{ID: 32121003, Level: 1, Stat: "TORNADO", ExpiresAt: now.Add(time.Minute)}}
	p, ongoing, err := skills.AttackResourceCost(c, 32121003, 1, protocol.RecvBodyAttack, now)
	if err != nil || !ongoing || p != (content.ResourceCost{}) {
		t.Fatal("paid ongoing attack charged twice", p, ongoing, err)
	}
	c.SkillBuffs = nil
	if _, _, err = skills.AttackResourceCost(c, 32121003, 1, protocol.RecvBodyAttack, now); err == nil {
		t.Fatal("forged ongoing cast free")
	}
	c.Job = 230
	c.Skills = []domain.SkillState{{ID: 2301002, Level: 1}}
	p, _, err = skills.AttackResourceCost(c, 2301002, 1, protocol.RecvMagicAttack, now)
	if err != nil || p.MP != 0 {
		t.Fatal("Heal cast MP paid again by attack", p, err)
	}
	c.Job = 3511
	c.Skills = []domain.SkillState{{ID: 35111004, Level: 1}}
	p, _, err = skills.AttackResourceCost(c, 35111004, 1, protocol.RecvShootAttack, now)
	if err != nil || p.Cooldown != 0 {
		t.Fatal("NoCooldown spec restarted", p, err)
	}
	if !reflect.DeepEqual(c.Equipment[0].Potential.Options, [5]uint16{10001}) {
		t.Fatal("potential changed")
	}
}

func TestResourceNativeExtraSourcesAndUnknownPassiveDoesNotDefault(t *testing.T) {
	c, now := resourceCostFixture(t)
	// Non-amplified buff casts still receive the potential reduction.
	p, err := skills.ResolveResourceCost(c, 2001002, 7, 0, now)
	if err != nil || p.MP != 5 {
		t.Fatal("buff incorrectly used passive amplification", p, err)
	}
	c.SkillBuffs = []domain.SkillBuff{{Stat: "TELEPORT_MASTERY", Value: 20, ExpiresAt: now.Add(time.Minute)}}
	p, err = skills.ResolveResourceCost(c, 2101002, 7, 0, now)
	if err != nil || p.MP != 19 {
		t.Fatal("teleport extra order", p, err)
	}
	c.Job = 3512
	c.Skills = []domain.SkillState{{ID: 35111004, Level: 1}, {ID: 35121005, Level: 1}}
	c.SkillBuffs = []domain.SkillBuff{{Stat: "MECH_CHANGE", Value: 1, ExpiresAt: now.Add(time.Minute)}, {Stat: "INFINITY", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	p, _, err = skills.AttackResourceCost(c, 35111004, 1, protocol.RecvShootAttack, now)
	if err != nil || p.MP != 4 || p.Cooldown != 0 {
		t.Fatal("mode W/infinity/reducer order", p, err)
	}
	c.Job = 2100
	c.Skills = []domain.SkillState{{ID: 20000093, Level: 1}}
	c.SkillBuffs = []domain.SkillBuff{{Stat: "INVINCIBILITY", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	p, err = skills.ResolveResourceCost(c, 2111002, 7, 0, now)
	if err != nil || p.MP != 0 {
		t.Fatal("hero invincibility reducer/clamp", p, err)
	}
	c.Job = 212
	c.Equipment = nil
	c.SkillBuffs = nil
	c.Skills = []domain.SkillState{{ID: 2110001, Level: 1}}
	// A book of the operator's own that lacks the passive: the built-in v148
	// book only stands in for a missing or stock file.
	empty := t.TempDir()
	if err = os.MkdirAll(filepath.Join(empty, "Skill.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(empty, "Skill.wz", "211.img.xml"), []byte(`<imgdir name="book"><imgdir name="skill"></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", empty)
	if _, err = skills.ResourceModifiers(c, now); err == nil {
		t.Fatal("missing passive source silently treated as100%")
	}
}
