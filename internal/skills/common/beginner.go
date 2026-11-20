package common

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// Beginner skills by suffix, registered for every family that has them
// (Java MapleStatEffect's isBeginnerJob(sourceid / 10000) switch).
func init() {
	// 嫩寶丟擲術／水晶投擲 Three Snails: fixdamage magic-family input.
	each(1000, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.AttackMagic}})
	each(1001, skills.Handler{Kind: skills.KindBuff, Cast: recovery})            // 治癒／潛入
	each(1002, skills.Handler{Kind: skills.KindBuff, Cast: skills.StandardBuff}) // 疾風之步 Nimble Feet: speed

	// 英雄的回響 Echo of Hero: ECHO_OF_HERO x. Legacy's WZ has no lt/rb, so
	// Java isPartyBuff is false and only the caster gets it. The Demon one
	// (30011005) costs forceCon Demon Force (StandardCost).
	each(1005, skills.Handler{Kind: skills.KindBuff, Cast: buff(0, key("ECHO_OF_HERO", "x"))})
	// Original v148 0x4C7E2D explicitly maps Mihile 50001215 to suffix1005.
	skills.Register(skills.Handler{ID: 50001215, Kind: skills.KindBuff, Cast: buff(0, key("ECHO_OF_HERO", "x"))})
	// Mihile 50001214 (騎士的監視 Knight's Watch, v148 0x93B056/0x6AA0FC): STANCE = prop.
	skills.Register(skills.Handler{ID: 50001214, Kind: skills.KindBuff, Cast: buff(0, key("STANCE", "prop"))})
	for _, id := range []int{10001075, 50001075} { // 女皇的祈禱 Empress's Prayer
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: buff(0, key("ECHO_OF_HERO", "x"))})
	}

	// Mu Lung Dojo energy skills. The v148 client offers them only on a
	// CField_Dojang floor with a full energy gauge (0x4FE6EC, 0x8F3165) and
	// refuses 1010/1011 elsewhere; the world checks and spends the energy.
	// 竹竿千擊 Bamboo Rain (x1009): a melee attack whose every line the client
	// sets to the target's maximum HP, or 30% of it on a boss (0x71160B).
	each(1009, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone,
		MaxDamage: bambooDamage, Prepare: dojangAttack}})
	// 法老的憤怒攻擊 Pharaoh's Wrath (x1020): v148 0x938AA8 dispatches melee
	// (0x92D0E9) and CalcDamage 0x711FC2 sets every line to 100% of target max HP.
	each(1020, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone,
		MaxDamage: pharaohDamage}})
	// Giant Potion attacks: v148 0x93B3B8/0x93B3C0 dispatch x1092 (jump) and
	// x1094 (swing) via melee (0x92D0E9); 0x925CDE dispatches x1095 via body
	// contact attack (0x9232A1, opcode 0x58).
	for _, suffix := range []int{1092, 1094} {
		each(suffix, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone}})
	}
	each(1095, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Body, Ammo: combat.AmmoNone}})
	// 金剛不壞 Invincible Barrier: DIVINE_BODY 1 for time.
	each(1010, skills.Handler{Kind: skills.KindBuff, Cast: dojangOnly(buff(0, fixed("DIVINE_BODY", 1)))})
	// 地火天爆 Power Explosion: BERSERK_FURY x (200) for time; v148 CalcDamage
	// multiplies physical lines by nBerserkFury/100 (0x713329) and doubles magic
	// lines when non-zero (0x708109).
	each(1011, skills.Handler{Kind: skills.KindBuff, Cast: dojangOnly(buff(0, key("BERSERK_FURY", "x")))})

	// 低等級怪物騎乘 Low-level mount: Java getMountItem returns 1932235, which
	// v145's WZ does not have, and the native client refuses the cast without
	// an equipped taming mob, so the equipped mount is ridden like 80001000.
	for _, id := range []int{1016, 10001017, 20001041} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff | skills.KindMorph, Cast: ride(equippedMount)})
	}
	// Fixed-item flying/glide mounts (v148 sub_6AB7A9 mount_item -> skill_id table):
	// 1203 鬥神降世 (1932103), 1204 海盜船 (1932102), 20010022 龍飛行 (1932016),
	// 30011109 魔族之翼 (1932051), 30011159 特化魔族之翼 (1932085).
	for _, m := range []struct{ id, item int }{
		{1203, 1932103}, {1204, 1932102}, {20010022, 1932016}, {30011109, 1932051}, {30011159, 1932085},
	} {
		item := m.item
		skills.Register(skills.Handler{ID: m.id, Kind: skills.KindBuff | skills.KindMorph, Cast: ride(func(domain.Character) int { return item })})
	}

	// Original v148 0x6AF12B resolves these casts through the equipped ring.
	for _, source := range []struct{ suffix, ring, attack int }{
		{1085, 1112585, 5}, {1087, 1112586, 10}, {1090, 1112594, 5}, {1179, 1112663, 12},
	} {
		each(source.suffix, skills.Handler{Kind: skills.KindSummon | skills.KindBuff, CastEquipment: source.ring,
			Cast: buff(skills.Permanent, fixed("ANGEL_ATK", source.attack), fixed("ANGEL_MATK", source.attack)), Attack: angel})
	}
	// 大天使祝福 etc.: Java maps indiePad/indieMad to ANGEL_ATK/ANGEL_MATK.
	for _, suffix := range []int{86, 88, 91, 180} {
		each(suffix, skills.Handler{Kind: skills.KindBuff, Cast: buff(0, key("ANGEL_ATK", "indiePad"), key("ANGEL_MATK", "indieMad"))})
	}

	// 冰騎士 Ice Knight: ICE_SKILL 1 and MORPH (WZ morph) for 2100000000 ms;
	// its attacks 97/99/100/103 need the transformation.
	each(1105, skills.Handler{Kind: skills.KindBuff | skills.KindMorph, Cast: buff(skills.Permanent, fixed("ICE_SKILL", 1), key("MORPH", "morph"))})
	each(97, skills.Handler{Kind: skills.KindAttack, Attack: ice(nil)})                                                                                                // 極冰突擊
	each(99, skills.Handler{Kind: skills.KindAttack | skills.KindMobStatus, Attack: ice(freeze)})                                                                      // 寒冰碎擊
	each(100, skills.Handler{Kind: skills.KindAttack | skills.KindMobStatus, Attack: ice(nil, combat.MobStatusApply{Stat: combat.MobPoison, Value: 1, Poison: true})}) // 極冰暴風
	each(103, skills.Handler{Kind: skills.KindAttack | skills.KindMobStatus, Attack: ice(nil, combat.MobStatusApply{Stat: combat.MobStun, Value: 1})})                 // 冰砍

	// Movement the client performs alone: pay the cost, show the cast.
	each(1026, skills.Handler{Kind: skills.KindSpecial | skills.KindBuff, Cast: soar, DropOnEnter: cancelSoaringOnEnter}) // 飛翔 Soaring
	for _, suffix := range []int{1006, 1068, 1098} {                                                                      // 向下跳躍、機甲戰神衝鋒、冰雙跳躍
		each(suffix, skills.Handler{Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}
	for _, id := range []int{30010110, 30010183, 30010184, 30010186} { // 惡魔跳躍 and its linked jumps
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}
	// 獵人的呼喚: Java's generic pad -> WATK for time.
	for _, id := range []int{20021062, 20031062, 30001062, 30011062} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: skills.StandardBuff})
	}

	// 勝利者的祝福 Victor's Blessing: party heal of 3–5 × hp% of the caster's
	// magic attack (Java makeHealHP for a skill with hp).
	each(1189, skills.Handler{Kind: skills.KindHeal | skills.KindPartyBuff, Cast: partyHeal})
	// 勝利者的旋風 Victor's Whirlwind: area melee, mobCount 20 (S0b settles at
	// most 15 targets per packet).
	each(1188, skills.Handler{Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee}})

	beginnerPassives()
}

// recovery (x001) heals x HP every five seconds for time seconds (Java
// RECOVERY); Resistance and Demon get INFILTRATE x instead. Java adds the
// generic speed statup, so 潛入 also carries SPEED.
func recovery(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("x") < 1 || c.Value("time") < 1 {
		return p, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	b := &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second}
	if f := c.Skill.ID / 10000; f == 3000 || f == 3001 {
		b.Stats = []skills.StatValue{{Stat: "INFILTRATE", Value: c.Value("x")}}
	} else {
		b.Tick = 5 * time.Second
		b.Stats = []skills.StatValue{{Stat: "RECOVERY", Value: c.Value("x")}}
	}
	b.Stats = append(b.Stats, skills.StandardStats(c.Values)...)
	p.Self = b
	return p, nil
}

// angel is the Angelic Blessing summon: Java FOLLOW movement; Java counts
// it as a puppet only for the spawn packet's summon type 0, it absorbs no
// attacks and never attacks. The WZ has no time, so the summon lasts like
// the buff (Java duration 2100000000).
var angel = &combat.AttackSpec{Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 2, Duration: skills.Permanent, Sub: angelSub}}

// freeze is Java's FREEZE 1 for twice the WZ time ("freezing skills are a
// little strange").
func freeze(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Value: 1, Duration: 2 * ctx.Effect.Time})
	return nil
}

// ice is an Ice Knight attack: only while transformed (ICE_SKILL). The wire
// family is not observed yet, so any input family is accepted; the ice gauge
// (iceGageCon) is not tracked.
func ice(prepare func(*combat.AttackContext) error, onHit ...combat.MobStatusApply) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Melee | combat.Shoot | combat.Magic, Ammo: combat.AmmoNone, OnHit: onHit, Prepare: func(ctx *combat.AttackContext) error {
		if _, ok := skills.ActiveStat(ctx.Character, "ICE_SKILL", ctx.Now); !ok {
			return fmt.Errorf("skill%d: needs Ice Knight", ctx.Effect.SkillID)
		}
		if prepare != nil {
			return prepare(ctx)
		}
		return nil
	}}
}

// ride is a mount (Java isMonsterRiding): MONSTER_RIDING carries the mount
// item for 2100000000 ms (Java giveMount sends the item as the value and the
// skill as the reason); recasting dismounts.
func ride(mount func(domain.Character) int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		item := mount(c.Caster)
		if item == 0 {
			return p, fmt.Errorf("skill%d: no mount", c.Skill.ID)
		}
		p.Self = &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "MONSTER_RIDING", Value: item}}}
		p.Toggle = true
		return p, nil
	}
}

// equippedMount is Java parseMountInfo_Pure for 1004/80001000: the taming
// mob in slot -18, only with a saddle in slot -19.
func equippedMount(c domain.Character) int {
	var mob, saddle int
	for _, e := range c.Equipment {
		switch e.Slot {
		case -18:
			mob = e.ItemID
		case -19:
			saddle = e.ItemID
		}
	}
	if saddle == 0 {
		return 0
	}
	return mob
}

// partyHeal heals every party member in range by 3–5 × hp% of the caster's
// magic attack, like magician 2301002.
func partyHeal(c *skills.CastContext) (skills.CastPlan, error) {
	stats, err := combat.AttackStats(c.Caster, c.Now)
	if err != nil {
		return skills.CastPlan{}, err
	}
	low, high := stats.MATK*3*c.Value("hp")/100, stats.MATK*5*c.Value("hp")/100
	p := skills.StandardCost(c)
	p.Heal = &skills.Heal{HP: low + rand.IntN(high-low+1)}
	return skills.PartyPlan(c, &skills.Buff{}, p)
}

// soar activates the v148 SOARING temporary stat. The client sends a normal
// cast, but its vertical controls only work after this stat arrives.
func soar(c *skills.CastContext) (skills.CastPlan, error) {
	canSoar, err := skills.CanSoar(c.Caster.MapID)
	if err != nil {
		return skills.CastPlan{}, err
	}
	if !canSoar {
		return skills.CastPlan{}, fmt.Errorf("skill%d: map %d cannot soar", c.Skill.ID, c.Caster.MapID)
	}
	p := skills.StandardCost(c)
	p.Toggle = true
	p.Self = &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "SOARING", Value: 1}}}
	if c.Skill.ID == skills.MihileSoaringID {
		p.Self.Stats[0].Value = c.Value("x")
		p.Self.Tick = time.Second
	}
	return p, nil
}

func cancelSoaringOnEnter(c domain.Character) bool {
	if c.MapID == content.DragonRiderTrap {
		return true
	}
	canSoar, err := skills.CanSoar(c.MapID)
	return err != nil || !canSoar
}

// dojangOnly refuses a dojo energy buff outside a CField_Dojang floor, as the
// client does. The world gates the energy gauge before the cast.
func dojangOnly(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if !content.DojangField(c.Caster.MapID) {
			return skills.CastPlan{}, fmt.Errorf("skill%d: Mu Lung Dojo floors only", c.Skill.ID)
		}
		return cast(c)
	}
}

func dojangAttack(ctx *combat.AttackContext) error {
	if !content.DojangField(ctx.Character.MapID) {
		return fmt.Errorf("skill%d: Mu Lung Dojo floors only", ctx.Effect.SkillID)
	}
	return nil
}

// bambooDamage is the v148 line value of x1009 (0x71160B): the target's
// template maximum HP (0x5F736D), or (int)(maxHP * 0.3) when it is a boss.
// Integer tenths are never below the client's floating-point product.
func bambooDamage(dc combat.DamageContext) int {
	hp := dc.Mob.HP
	if dc.Mob.Boss {
		hp = hp * 3 / 10
	}
	return int(min(max(hp, 0), int64(1<<31-1)))
}

// pharaohDamage is the v148 line value of x1020 (0x711FC2 -> 0x711FB8):
// 100% of the target's template maximum HP (WZ damagepc=100), with no boss cap.
func pharaohDamage(dc combat.DamageContext) int {
	return int(min(max(dc.Mob.HP, 0), int64(1<<31-1)))
}
