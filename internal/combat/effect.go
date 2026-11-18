package combat

import (
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// FromSkill builds the attack Effect for one learned level from the WZ skill
// template (Java MapleStatEffect fields), including the level or common lt/rb
// box. elemAttr is not read: Element stays Physical unless a skill sets it.
func FromSkill(s content.Skill, level int) (Effect, error) {
	if s.Disabled {
		return Effect{}, fmt.Errorf("skill%d disabled", s.ID)
	}
	v, err := s.Values(level)
	if err != nil {
		return Effect{}, err
	}
	sec := func(k string) time.Duration { return time.Duration(v[k]) * time.Second }
	e := Effect{
		SkillID: s.ID, Level: level,
		Damage: v["damage"], FixDamage: v["fixdamage"],
		MobCount: v["mobCount"], AttackCount: v["attackCount"],
		MPCon: v["mpCon"], HPCon: v["hpCon"],
		BulletCount: v["bulletCount"], BulletConsume: v["bulletConsume"],
		ItemCon: v["itemCon"], ItemConNo: v["itemConNo"], MoneyCon: v["moneyCon"],
		Cooltime: sec("cooltime"), Time: sec("time"), SubTime: sec("subTime"),
		Prop: v["prop"], X: v["x"], Y: v["y"], Z: v["z"],
		DOT: v["dot"], DOTTime: sec("dotTime"), DOTInterval: sec("dotInterval"),
		Range: v["range"], Mastery: v["mastery"], CriticalRate: v["cr"],
		IgnoreMobDefense: v["ignoreMobpdpR"], MAD: v["mad"],
	}
	if r, ok := s.RangeAt(level); ok {
		e.Box = Box{LT: Point{r.LT[0], r.LT[1]}, RB: Point{r.RB[0], r.RB[1]}, Set: true}
	}
	if v["forceCon"] > 0 {
		// Demon Force (DF) replaces MP for Demon Slayer skills.
		e.MPCon = v["forceCon"]
		e.ForceCost = true
	}
	if e.Damage == 0 && e.FixDamage == 0 {
		e.Damage = 100
	}
	if e.MobCount == 0 {
		e.MobCount = s.SummonAttack["mobCount"]
	}
	if e.AttackCount == 0 {
		e.AttackCount = s.SummonAttack["attackCount"]
	}
	if e.MobCount == 0 {
		e.MobCount = 1
	}
	if e.AttackCount == 0 {
		e.AttackCount = 1
	}
	if e.MPCon < 0 || e.HPCon < 0 || e.MobCount > 100 || e.AttackCount > 15 || e.Damage < 0 || e.Damage > 100000 {
		return Effect{}, fmt.Errorf("skill%d level%d attack values out of range", s.ID, level)
	}
	return e, nil
}

// Lines is the line limit per target: shoot attacks fire max(bulletCount,
// attackCount) like Java PlayerHandler.rangedAttack.
func (e Effect) Lines(k AttackKind) int {
	if k == Shoot {
		return max(e.AttackCount, e.BulletCount)
	}
	return e.AttackCount
}

// NormalEffect is the ordinary (skill 0) attack.
func NormalEffect() Effect {
	return Effect{Damage: 100, MobCount: 1, AttackCount: 1}
}

// NormalEffectFor is the ordinary attack of c: Java PlayerHandler.closeRangeAttack
// gives two hits when a katara (Dual Blade 134xxxx) is in the shield slot.
func NormalEffectFor(c domain.Character) Effect {
	e := NormalEffect()
	for _, it := range c.Equipment {
		if it.Slot == -10 && it.ItemID/10000 == 134 {
			e.AttackCount = 2
		}
	}
	return e
}

// BaseStats includes equipment and intrinsic job stats before the StatsProvider
// adds passives and buffs. Cash appearance slots never add stats.
func BaseStats(c domain.Character, _ time.Time) (PlayerStats, error) {
	p := PlayerStats{Job: c.Job, Level: c.Level, STR: c.STR, DEX: c.DEX, INT: c.INT, LUK: c.LUK}
	for _, e := range c.Equipment {
		if e.Slot >= 0 || e.Slot <= -100 {
			continue
		}
		p.STR += e.STR
		p.DEX += e.DEX
		p.INT += e.INT
		p.LUK += e.LUK
		p.WATK += e.WATK
		p.MATK += e.MATK
		p.Accuracy += e.ACC
		switch e.Slot {
		case -11:
			p.WeaponID = e.ItemID
		case -10:
			p.SecondaryWeaponID = e.ItemID
		}
	}
	if p.STR < 0 || p.DEX < 0 || p.INT < 0 || p.LUK < 0 || p.WATK < 0 || p.MATK < 0 {
		return p, fmt.Errorf("invalid attack stats")
	}
	_, projectileAttack := content.ZenProjectile(c.Job)
	p.WATK += projectileAttack
	return p, nil
}

// chargeElements maps WK_CHARGE sources to the attack element (Java
// DamageParse): Fire/Ice/Lightning/Holy charges, Thunder Charge (party) and
// Aran's Snow Charge.
var chargeElements = map[int]Element{
	1211004: Fire, 1211006: Ice, 1211008: Lightning, 15101006: Lightning,
	1221004: Holy, 11111007: Holy, 21111005: Ice,
}

// ChargeElement returns the element of the attacker's active WK_CHARGE buff.
func ChargeElement(c domain.Character, now time.Time) (Element, bool) {
	for _, b := range c.SkillBuffs {
		if b.Stat == "WK_CHARGE" && b.ExpiresAt.After(now) {
			if e, ok := chargeElements[b.ID]; ok {
				return e, true
			}
		}
	}
	return Physical, false
}
