package pirate

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/skills"
)

func init() {
	for _, id := range []int{5121008, 5221010, 5321006, 5721002, 5821008} { // 楓葉淨化 Hero's Will
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: skills.HerosWill})
	}
	skills.Register(skills.Handler{ID: 5201005, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // 緩降術 Wings: Java has no buff stat
	for _, id := range []int{5121010, 5821010} {                                                  // 時間置換 Time Leap
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPartyBuff, Cast: timeLeap})
	}
	skills.Register(skills.Handler{ID: 5101005, Kind: skills.KindHeal, Cast: mpRecovery})                    // 魔力再生 MP Recovery
	skills.Register(skills.Handler{ID: 5221006, Kind: skills.KindBuff | skills.KindMorph, Cast: battleship}) // 海盜船 Battleship
}

// timeLeap clears party cooldowns in range while keeping both original Time Leap variants.
func timeLeap(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.ResetCooldowns = true
	p.KeepCooldowns = []int{5121010, 5821010}
	return skills.PartyPlan(c, &skills.Buff{}, p)
}

// mpRecovery (5101005) trades 10% of MaxHP for y percent of it as MP (Java
// isMPRecovery, with its integer rounding).
func mpRecovery(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	hp := c.Caster.MaxHP / 100 * 10
	if hp < 1 || c.Caster.HP <= hp {
		return p, fmt.Errorf("skill5101005: not enough HP")
	}
	p.HPCost = hp
	p.Heal = &skills.Heal{MP: hp / 100 * c.Value("y")}
	return p, nil
}

// battleshipMount is the mount item of 5221006 (Java
// GameConstants.getMountItem); the client rides only when MONSTER_RIDING
// carries it.
const battleshipMount = 1932000

// battleship (5221006) is a mount (Java isMonsterRiding: MONSTER_RIDING with
// the mount item, sent by giveMount, for 2100000000 ms) with STANCE 100 first
// and the WZ enhanced stats. Recasting dismounts.
func battleship(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	stats := []skills.StatValue{{Stat: "STANCE", Value: 100}, {Stat: "MONSTER_RIDING", Value: battleshipMount}}
	for _, s := range []struct {
		key  string
		stat buffstat.Stat
	}{{"epad", "ENHANCED_WATK"}, {"epdd", "ENHANCED_WDEF"}, {"emdd", "ENHANCED_MDEF"}, {"emhp", "ENHANCED_MAXHP"}, {"emmp", "ENHANCED_MAXMP"}} {
		if v := c.Value(s.key); v != 0 {
			stats = append(stats, skills.StatValue{Stat: s.stat, Value: v})
		}
	}
	p.Self = &skills.Buff{Duration: skills.Permanent, Stats: stats}
	p.Toggle = true
	return p, nil
}
