package content

import (
	"fmt"
	"path/filepath"
	"sync"
)

type MesoDropRule struct {
	Count, Chance, Minimum, Maximum int
	Explosive                       bool
}

var mesoRules sync.Map

// MonsterMesoRule follows MapleMonsterStats.dropsMeso and the ordinary WZ
// branch of MonsterInformationProvider.addMeso. Custom party-play overrides
// remain separate content; missing source facts do not invent a money drop.
func MonsterMesoRule(root string, mobID int) (MesoDropRule, error) {
	key := fmt.Sprintf("%s/%d", root, mobID)
	if rule, ok := mesoRules.Load(key); ok {
		return rule.(MesoDropRule), nil
	}
	if root == "" {
		return MesoDropRule{}, nil
	}
	group := mobID / 100000
	if group == 97 || group == 95 || group == 93 || group == 91 || group == 90 {
		return MesoDropRule{}, nil
	}
	node, err := readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", mobID)))
	if err != nil {
		return MesoDropRule{}, err
	}
	info := node.child("info")
	if info == nil {
		return MesoDropRule{}, fmt.Errorf("monster has no meso rule info")
	}
	if linked := info.intValue("link", 0); linked > 0 && linked != mobID {
		node, err = readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", linked)))
		if err != nil {
			return MesoDropRule{}, err
		}
		info = node.child("info")
	}
	if info == nil || info.intValue("removeAfter", 0) != 0 || info.intValue("invincible", 0) != 0 ||
		info.intValue("onlyNormalAttack", 0) != 0 || info.intValue("dropItemPeriod", 0) > 0 ||
		info.intValue("getCP", 0) > 0 || info.intValue("point", 0) > 0 || info.intValue("fixedDamage", -1) > 0 ||
		info.intValue("PDRate", 0) <= 0 || info.intValue("MDRate", 0) <= 0 ||
		info.child("selfDestruction").intValue("action", -1) != -1 {
		mesoRules.Store(key, MesoDropRule{})
		return MesoDropRule{}, nil
	}
	level := info.intValue("level", 0)
	if level < 1 || level > 1000 {
		return MesoDropRule{}, fmt.Errorf("invalid monster level for meso rule")
	}
	boss, party := info.intValue("boss", 0) != 0, info.intValue("partyBonusMob", 0) != 0
	rule := MesoDropRule{Count: 1, Chance: 600000, Maximum: level, Explosive: info.intValue("explosiveReward", 0) != 0}
	if level >= 100 {
		rule.Maximum = level * 10
	} else if level >= 10 {
		rule.Maximum = level * ((level + 9) / 10)
	}
	if boss {
		rule.Count = 2
		if !party {
			rule.Chance, rule.Maximum = 800000, level*level
		}
	}
	if party {
		rule.Chance = 500000
	}
	if rule.Explosive {
		rule.Count = 7
	}
	rule.Minimum = rule.Maximum * 66 / 100
	mesoRules.Store(key, rule)
	return rule, nil
}
