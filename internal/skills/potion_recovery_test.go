package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func potionSkillWZ(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	files := map[string]string{
		"Item.wz/ItemOption.img.xml":           `<imgdir name="ItemOption.img"><imgdir name="030551"><imgdir name="info"><int name="weight" value="5"/><int name="optionType" value="11"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryUP" value="10"/></imgdir></imgdir></imgdir><imgdir name="040551"><imgdir name="info"><int name="weight" value="5"/><int name="optionType" value="11"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryUP" value="20"/></imgdir></imgdir></imgdir></imgdir>`,
		"Character.wz/Weapon/01302000.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`,
	}
	for _, id := range []int{4110000, 14110003, 30000002} {
		files[fmt.Sprintf("Skill.wz/%03d.img.xml", id/10000)] = fmt.Sprintf(`<imgdir name="book"><imgdir name="skill"><imgdir name="%d"><int name="combatOrders" value="1"/><imgdir name="common"><int name="maxLevel" value="20"/><string name="x" value="100+u(x*5)"/></imgdir></imgdir></imgdir></imgdir>`, id)
	}
	for name, data := range files {
		p := filepath.Join(r, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", r)
	return r
}

func TestPotionRecoveryCheckedBucketEligibilityExpiryAndOpaqueData(t *testing.T) {
	potionSkillWZ(t)
	now := time.Unix(1800000000, 0)
	c := domain.Character{Job: 411, Skills: []domain.SkillState{{ID: 4110000, Level: 2}}, Equipment: []domain.Equipment{{ID: 1, ItemID: 1302000, Slot: -11, Potential: domain.EquipmentPotential{Present: true, RawState: 19, Options: [5]uint16{30551, 40551}}}}}
	m, err := PotionRecoveryModifiersChecked(c, now)
	if err != nil || m.FixedHPPercent != 40 {
		t.Fatal(m, err)
	}
	// Both potential lines and the passive add to one bucket, never multiply.
	c.SkillBuffs = []domain.SkillBuff{{Stat: "COMBAT_ORDERS", Value: 1, ExpiresAt: now.Add(time.Second)}}
	m, err = PotionRecoveryModifiersChecked(c, now)
	if err != nil || m.FixedHPPercent != 45 {
		t.Fatal("effective passive level", m, err)
	}
	c.SkillBuffs[0].ExpiresAt = now
	c.Skills[0].ExpiresAt = now
	c.Equipment[0].ExpiresAt = now.UnixMilli()
	m, err = PotionRecoveryModifiersChecked(c, now)
	if err != nil || m.FixedHPPercent != 0 {
		t.Fatal("expired sources", m, err)
	}
	for _, tc := range []struct {
		job, id int
		want    int
	}{{411, 4110000, 10}, {412, 4110000, 10}, {410, 4110000, 0}, {1411, 14110003, 10}, {1400, 14110003, 10}, {1410, 14110003, 10}, {100, 14110003, 0}, {3200, 30000002, 10}, {3112, 30000002, 10}, {100, 30000002, 0}} {
		m, err = PotionRecoveryModifiersChecked(domain.Character{Job: tc.job, Skills: []domain.SkillState{{ID: tc.id, Level: 2}}}, now)
		if err != nil || m.FixedHPPercent != tc.want {
			t.Fatal(tc, m, err)
		}
	}
	c.Equipment[0].ExpiresAt = 0
	for _, word := range []uint16{0, 30551} {
		c.Equipment[0].Potential.RawState = 0x15
		c.Equipment[0].Potential.Options[0] = word
		if _, err = PotionRecoveryModifiersChecked(c, now); err == nil {
			t.Fatal("unknown tier silently removed", word)
		}
	}
	c.Equipment = nil
	c.Skills = []domain.SkillState{{ID: 4110000, Level: 21}}
	if _, err = PotionRecoveryModifiersChecked(c, now); err == nil {
		t.Fatal("invalid learned passive clamped")
	}
}

func TestPotionRecoveryOriginalWZPassiveCapsAndCombatOrders(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "wz"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "Skill.wz", "411.img.xml")); err != nil {
		t.Skip("original mounted Skill.wz absent")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct{ job, id, level, co, want int }{
		{411, 4110000, 20, 0, 50}, {411, 4110000, 20, 2, 50},
		{1400, 14110003, 20, 0, 50}, {1410, 14110003, 20, 0, 50}, {1411, 14110003, 20, 2, 54},
		{3000, 30000002, 3, 2, 15}, {3001, 30000002, 3, 2, 15}, {3112, 30000002, 3, 0, 15},
	} {
		c := domain.Character{Job: tc.job, Skills: []domain.SkillState{{ID: tc.id, Level: tc.level}}, SkillBuffs: []domain.SkillBuff{{Stat: "COMBAT_ORDERS", Value: tc.co, ExpiresAt: now.Add(time.Minute)}}}
		m, err := PotionRecoveryModifiersChecked(c, now)
		if err != nil || m.FixedHPPercent != tc.want {
			t.Fatal("original WZ", tc, m, err)
		}
	}
}
