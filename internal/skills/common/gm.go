package common

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"time"
)

// GM skills (job 800 and 900). The WZ gives none of them lt/rb, so Java
// isPartyBuff is false and every buff only reaches the caster. Legacy bans a
// non-GM who casts 9001000, 9001001, 9001002 or 9001004 (gmOnly).
func init() {
	skills.Register(skills.Handler{ID: 8001000, Kind: skills.KindSpecial, Cast: gmOnly(skills.CostOnly)})                    // 測謊機 Lie Detector
	skills.Register(skills.Handler{ID: 8001001, Kind: skills.KindSpecial, Cast: skills.CostOnly})                            // 瞬間移動 Teleport
	skills.Register(skills.Handler{ID: 9001005, Kind: skills.KindPartyBuff | skills.KindHeal, Cast: gmOnly(gmResurrection)}) // 復活 GM Resurrection
	skills.Register(skills.Handler{ID: 9001007, Kind: skills.KindSpecial, Cast: skills.CostOnly})                            // 終極瞬移 GM Teleport
	skills.Register(skills.Handler{ID: 9001009, Kind: skills.KindSpecial, Cast: gmOnly(skills.CostOnly)})                    // 測謊機 GM Lie Detector
	skills.Register(skills.Handler{ID: 9001000, Kind: skills.KindHeal, Cast: gmOnly(skills.HerosWill)})                      // 終極祝福: Java isDispel, prop 100
	skills.Register(skills.Handler{ID: 9001001, Kind: skills.KindBuff, Cast: gmOnly(skills.StandardBuff)})                   // 終極輕功 Haste: speed, jump
	skills.Register(skills.Handler{ID: 9001002, Kind: skills.KindBuff, Cast: gmOnly(buff(0, key("HOLY_SYMBOL", "x")))})      // 終極祈禱 Holy Symbol
	skills.Register(skills.Handler{ID: 9001003, Kind: skills.KindBuff, Cast: bless})                                         // 終極祝福 Bless
	skills.Register(skills.Handler{ID: 9001008, Kind: skills.KindBuff, Cast: buff(0, key("MAXHP", "x"), key("MAXMP", "x"))}) // Hyper Body: Java uses x for both
	skills.Register(skills.Handler{ID: 9001006, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee}})    // 終極龍咆哮
	skills.Register(skills.Handler{ID: 9001004, Kind: skills.KindBuff, Cast: gmOnly(hide)})                                  // 終極隱藏 GM Hide
}

// gmResurrection (9001005) revives dead party members in its WZ lt/rb range
// with full HP, matching Bishop Resurrection (2321006).
func gmResurrection(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Revive = true
	return skills.PartyPlan(c, &skills.Buff{}, p)
}

// hide is GM Hide: Java toggleHide for 2 hours (duration 60*120*1000); the
// core hides a GM_HIDE holder from the map. Recasting unhides.
func hide(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Self = &skills.Buff{Duration: 2 * time.Hour, Stats: []skills.StatValue{{Stat: "GM_HIDE", Value: 1}}}
	p.Toggle = true
	return p, nil
}

// bless is GM Bless: Java's generic stats plus BLESS = skill level.
func bless(c *skills.CastContext) (skills.CastPlan, error) {
	p, err := skills.StandardBuff(c)
	if err != nil {
		return p, err
	}
	p.Self.Stats = append(p.Self.Stats, skills.StatValue{Stat: "BLESS", Value: c.Level})
	return p, nil
}
