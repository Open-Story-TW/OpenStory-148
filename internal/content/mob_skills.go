package content

import (
	"path/filepath"
	"strconv"
	"time"
)

const MobSkillPowerUp = 100

type MobSkillAction struct {
	ID, Level, Action byte
	EffectAfter       time.Duration
}

type MobPowerUp struct {
	Power, MP, HPPercent int
	Duration, Cooldown   time.Duration
}

func parseMobSkillActions(node *wzNode) (actions []MobSkillAction) {
	if node == nil {
		return nil
	}
	for _, row := range node.Children {
		id, level, action := row.intValue("skill", 0), row.intValue("level", 0), row.intValue("action", -1)
		delay := row.intValue("effectAfter", 0)
		if id == MobSkillPowerUp && level > 0 && level <= 255 && action >= 1 && action <= 106 && delay >= 0 && delay <= 30000 {
			actions = append(actions, MobSkillAction{byte(id), byte(level), byte(action), time.Duration(delay) * time.Millisecond})
		}
	}
	return actions
}

// PowerUp returns a skill's values from the installed v148 MobSkill book.
func (c *Catalog) PowerUp(level byte) (MobPowerUp, bool) {
	c.mobSkillsOnce.Do(func() {
		book, err := readWZ(filepath.Join(c.root, "Skill.wz", "MobSkill.img.xml"))
		if err != nil {
			return
		}
		ranks := book.child("100").child("level")
		if ranks == nil {
			return
		}
		c.mobSkills = make(map[int]MobPowerUp, len(ranks.Children))
		for _, row := range ranks.Children {
			rank, err := strconv.Atoi(row.Name)
			power, mp, hp := row.intValue("x", 0), row.intValue("mpCon", 0), row.intValue("hp", 100)
			duration, cooldown := row.intValue("time", 0), row.intValue("interval", 0)
			if err != nil || rank < 1 || rank > 255 || power < 1 || power > 32767 ||
				mp < 0 || mp > 65535 || hp < 1 || hp > 100 || duration < 1 || duration > 86400 || cooldown < 1 || cooldown > 86400 ||
				row.intValue("once", 0) != 0 || row.child("prop") != nil {
				continue
			}
			c.mobSkills[rank] = MobPowerUp{power, mp, hp, time.Duration(duration) * time.Second, time.Duration(cooldown) * time.Second}
		}
	})
	value, ok := c.mobSkills[int(level)]
	return value, ok
}
