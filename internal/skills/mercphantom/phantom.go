package mercphantom

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

// Phantom attacks use the close-range packet (Java parseCloseRangeAttack
// handles 24121000's charge and 24121005's extra field). The cards of Carte
// Blanche/Noir are accepted from either family until MCP records them: S0b
// found the v145 client throws them itself (its own prop roll and 24100003/
// 24120002 attack), so the server only judges those attacks; 0xE2 is the
// Demon Force orb path and is not sent for Phantoms.
var phantomAttacks = map[int]rule{
	// 幻影 2400
	24001000: {kind: combat.Melee}, // 雙重刺擊 Double Piercing
	// 2410
	24100003: {kind: either, mobCount: 6},                  // 卡牌：白 Carte Blanche (Java mobCount 6)
	24101000: {kind: combat.Melee},                         // 命運召喚 Call of Fate
	24101002: {kind: combat.Melee, boost: []int{24111006}}, // 騎士衝擊 Knight Engaging (Phantom Charge damR)
	// 2411
	24111000: {kind: combat.Melee},                                              // 紋章 Coat of Arms
	24111006: {kind: combat.Melee, boost: []int{24121003}},                      // 幻影衝鋒 Phantom Charge (Twilight damR)
	24111008: {kind: combat.Melee, boost: []int{24121003}, levelFrom: 24111006}, // (invisible) Breeze Carte, linked to 24111006
	// 2412
	24120002: {kind: either},                            // 卡牌：黑 Carte Noir
	24121000: {kind: combat.Melee},                      // 千針 Mille Aiguilles (keydown charge)
	24121005: {kind: combat.Melee},                      // 卡牌風暴 Tempest of Card (keydown loop; WZ mobCount 20)
	24121010: {kind: combat.Melee, levelFrom: 24121003}, // (invisible) Twilight finish, linked to 24121003
}

func init() {
	for id, r := range phantomAttacks {
		register(id, r)
	}

	passive(24000003, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{AvoidRate: c.Values["er"]} }) // 迅捷閃避 Quick Evasion (Legacy reads x, absent in WZ)
	passive(24000004, nil)                                                                                            // invisible link skill: no PvE effect
	passive(24100004, weaponMastery(cane, 24120006))                                                                  // 手杖精通 Cane Mastery
	passive(24100006, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{LUK: c.Values["lukX"]} })     // 幸運獨佔 Luck Monopoly
	passive(24110004, nil)                                                                                            // 閃光逃脫 Flash and Flee: Legacy no effect
	passive(24110007, func(c *skills.PassiveContext) skills.Bonus {                                                   // 敏銳直覺 Acute Sense
		return skills.Bonus{Critical: c.Values["cr"], CriticalDamageMin: c.Values["criticaldamageMin"]}
	})
	passive(24120006, expertMastery(cane)) // 手杖專家 Cane Expert

	skills.Register(skills.Handler{ID: 24111002, Kind: skills.KindPassive | skills.KindSpecial, Passive: luckOfPhantomThief, OnLethal: finalFeint})                                                          // 幻影神偷之運 Luck of Phantom Thief
	skills.Register(skills.Handler{ID: 24001002, Kind: skills.KindSpecial | skills.KindPassive, Cast: skills.CostOnly, Passive: skills.MovementPassive})                                                     // 幻影疾行 Swift Phantom
	skills.Register(skills.Handler{ID: 24101005, Kind: skills.KindBuff, Cast: buff(cane, key("BOOSTER", "x"))})                                                                                              // 手杖推進器 Cane Booster
	skills.Register(skills.Handler{ID: 24111003, Kind: skills.KindBuff, Cast: buff(0, key("MAXHP", "indieMhpR"), key("MAXMP", "indieMmpR"), key("ABNORMAL_STATUS_R", "x"), key("ELEMENTAL_STATUS_R", "y"))}) // 厄運守護 Bad Luck Ward (Legacy has no statups; WZ values)
	skills.Register(skills.Handler{ID: 24111005, Kind: skills.KindBuff, Cast: buff(0, key("ANGEL_ATK", "indiePad", "indiepad"), key("ANGEL_ACC", "indieAcc"))})                                              // 月光祝福 Moon Light (WZ spells it indiepad)
	skills.Register(skills.Handler{ID: 24121003, Kind: skills.KindAttack, Attack: attack(rule{kind: combat.Melee})})                                                                                         // 暮光 Twilight: casterMove attack, damR passive for 24111006/24111008
	skills.Register(skills.Handler{ID: 24121004, Kind: skills.KindBuff, Cast: buff(0, fixed("ENHANCED_WATK", 30))})                                                                                          // 艾莉亞的祈禱 Priere D'Aria (Legacy fixes ENHANCED_WATK 30; WZ damR unused)
	skills.Register(skills.Handler{ID: 24121007, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: &combat.AttackSpec{MobCast: &combat.MobCastSpec{Dispel: true, UseBox: true}}})                   // 靈魂竊取 Soul Steal
	skills.Register(skills.Handler{ID: 24121008, Kind: skills.KindPartyBuff, Cast: partyBuff(key("MAPLE_WARRIOR", "x"))})                                                                                    // 楓葉祝福 Maple Warrior
	skills.Register(skills.Handler{ID: 24121009, Kind: skills.KindSpecial, Cast: skills.HerosWill})                                                                                                          // 勇士的意志 Hero's Will
}

// luckOfPhantomThief (24111002) is Java PlayerStats localluk += lukX.
func luckOfPhantomThief(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{LUK: c.Values["lukX"]}
}

// finalFeint (24111002): a killing monster hit leaves the Phantom standing
// where they are with x% HP, then the WZ cooltime starts (WZ x = 5×level,
// cooltime 600-15×level; Legacy has no handler). Off cooldown only.
func finalFeint(c *skills.TriggerContext) *skills.BuffChange {
	if !domain.IsPhantomJob(c.Character.Job) || c.Value("x") < 1 {
		return nil
	}
	for _, cd := range c.Character.SkillCooldowns {
		if cd.ID == c.Skill.ID && cd.ExpiresAt.After(c.Now) {
			return nil
		}
	}
	return &skills.BuffChange{ReviveHPPercent: min(c.Value("x"), 100), Cooldown: time.Duration(c.Value("cooltime")) * time.Second}
}
