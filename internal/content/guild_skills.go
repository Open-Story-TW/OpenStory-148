package content

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
)

// GuildSkill holds Skill.wz/9100.img common formulas (x = skill level).
type GuildSkill struct {
	ID            int
	MaxLevel      int
	Price         string
	ExtendPrice   string
	ReqGuildLevel string
	Period        string // minutes
	PriceUnit     int
}

// GuildSkillLevel is one level's evaluated purchase/extension terms.
type GuildSkillLevel struct {
	Level         int
	Price         int64
	ExtendPrice   int64
	ReqGuildLevel int
	PeriodMinutes int
}

// Legacy Skill.wz/9100.img, used when no WZ root is configured.
var embeddedGuildSkills = map[int]GuildSkill{
	91000000: {91000000, 5, "160*x*x*x", "250+150*x", "-1+2*x", "60", 10000},
	91000001: {91000001, 5, "200*x*x*x", "250+150*x", "2*x", "30", 10000},
	91000002: {91000002, 5, "100*x*x*x", "250+50*x", "-1+2*x", "60", 10000},
	91000003: {91000003, 5, "200*x*x*x", "250+150*x", "2*x", "30", 10000},
	91000004: {91000004, 4, "100*x*x*x", "300+50*x", "1+2*x", "60", 10000},
	91000005: {91000005, 4, "160*x*x*x", "400+150*x", "2+2*x", "30", 10000},
	91000006: {91000006, 10, "1000*x", "", "x", "", 10000},
}

var guildSkillDefinitions sync.Map

func GuildSkillDefinition(root string, id int) (GuildSkill, error) {
	if id < 91000000 || id > 91009999 {
		return GuildSkill{}, fmt.Errorf("not a guild skill %d", id)
	}
	if root == "" {
		s, ok := embeddedGuildSkills[id]
		if !ok {
			return GuildSkill{}, fmt.Errorf("unknown guild skill %d", id)
		}
		return s, nil
	}
	key := fmt.Sprintf("%s/%d", root, id)
	if v, ok := guildSkillDefinitions.Load(key); ok {
		return v.(GuildSkill), nil
	}
	n, err := readWZ(filepath.Join(root, "Skill.wz", "9100.img.xml"))
	if err != nil {
		return GuildSkill{}, err
	}
	common := n.child("skill").child(strconv.Itoa(id)).child("common")
	if common == nil {
		return GuildSkill{}, fmt.Errorf("unknown guild skill %d", id)
	}
	s := GuildSkill{ID: id, MaxLevel: common.intValue("maxLevel", 0), Price: common.stringValue("price", ""), ExtendPrice: common.stringValue("extendPrice", ""), ReqGuildLevel: common.stringValue("reqGuildLevel", ""), Period: common.stringValue("period", ""), PriceUnit: common.intValue("priceUnit", 1)}
	if s.MaxLevel < 1 || s.MaxLevel > 30 || s.PriceUnit < 1 {
		return GuildSkill{}, fmt.Errorf("invalid guild skill %d", id)
	}
	guildSkillDefinitions.Store(key, s)
	return s, nil
}

func optionalFormula(source string, level int) (int, error) {
	if source == "" {
		return 0, nil
	}
	return SkillFormula(source, level)
}

// At evaluates one level, matching MapleStatEffect price*priceUnit.
func (s GuildSkill) At(level int) (GuildSkillLevel, error) {
	if level < 1 || level > s.MaxLevel {
		return GuildSkillLevel{}, fmt.Errorf("guild skill level %d out of range", level)
	}
	out := GuildSkillLevel{Level: level}
	price, err := optionalFormula(s.Price, level)
	if err != nil {
		return out, err
	}
	extend, err := optionalFormula(s.ExtendPrice, level)
	if err != nil {
		return out, err
	}
	if out.ReqGuildLevel, err = optionalFormula(s.ReqGuildLevel, level); err != nil {
		return out, err
	}
	if out.PeriodMinutes, err = optionalFormula(s.Period, level); err != nil {
		return out, err
	}
	out.Price, out.ExtendPrice = int64(price)*int64(s.PriceUnit), int64(extend)*int64(s.PriceUnit)
	return out, nil
}
