package bowman

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"time"
)

// SummonMovementType values (Java MapleStatEffect.getSummonMovementType).
const (
	stationary   = 0
	circleFollow = 4
)

// summon is one bowman summon (Java MapleSummon). Birds are attackers that
// stun (Java monsterStatus STUN 1); puppets absorb monster attacks. Elite
// Puppet attacks too (Java getSummonType excludes 3120012/3220012 from 0).
type summon struct {
	id      int
	stat    buffstat.Stat // Java statup of the summon buff.
	move    uint8
	attacks bool
	puppet  bool
	element combat.Element
	link    int // Spirit Link applied with the summon buff (Java applyBuffEffect).
}

var summons = []summon{
	{id: 3101007, stat: "SUMMON", move: circleFollow, attacks: true},                                      // 銀鷹召喚 Silver Hawk
	{id: 3111002, stat: "PUPPET", move: stationary, puppet: true},                                         // 替身術 Puppet
	{id: 3111005, stat: "SUMMON", move: circleFollow, attacks: true, element: combat.Fire, link: 3120006}, // 召喚鳳凰 Phoenix
	{id: 3120012, stat: "PUPPET", move: stationary, attacks: true, puppet: true},                          // 精銳替身術 Elite Puppet
	{id: 3201007, stat: "SUMMON", move: circleFollow, attacks: true},                                      // 金鷹召喚 Golden Eagle
	{id: 3211002, stat: "PUPPET", move: stationary, puppet: true},                                         // 替身術 Puppet
	{id: 3211005, stat: "SUMMON", move: circleFollow, attacks: true, element: combat.Ice, link: 3220005},  // 召喚銀隼 Frostprey
	{id: 3220012, stat: "PUPPET", move: stationary, attacks: true, puppet: true},                          // 精銳替身術 Elite Puppet
}

func init() {
	for _, s := range summons {
		spec := &combat.AttackSpec{Summon: &combat.SummonSpec{MoveAbility: s.move, Puppet: s.puppet}}
		kind := skills.KindSummon
		if s.attacks {
			spec.Kind = combat.Summoned
			spec.Summon.AssistType = 1
			spec.Prepare = element(s.element)
			if !s.puppet {
				spec.OnHit = stun
				kind |= skills.KindMobStatus
			}
		}
		skills.Register(skills.Handler{ID: s.id, Kind: kind, Cast: summonBuff(s.stat, s.link), Attack: spec})
	}
}

// summonBuff is the summon's buff: stat 1 for the WZ time; the core spawns
// the summon from the registered spec. With link learned (Phoenix/Frostprey
// and Spirit Link) the buff also carries Java's 3120006/3220005 statups
// SPIRIT_LINK 1, ELEMENTAL_STATUS_R terR and DAMAGE_BUFF damage for the
// summon's duration; spiritLink adds the passive part.
func summonBuff(stat buffstat.Stat, link int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		if c.Value("time") < 1 {
			return p, fmt.Errorf("skill%d: no duration", c.Skill.ID)
		}
		b := &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{{Stat: stat, Value: 1}}}
		if lv := learned(c.Caster, link); link != 0 && lv > 0 {
			def, err := content.SkillDefinition("", link)
			if err != nil {
				return p, err
			}
			v, err := def.Values(min(lv, def.MaxLevel))
			if err != nil {
				return p, err
			}
			for _, s := range []skills.StatValue{{Stat: "SPIRIT_LINK", Value: 1}, {Stat: "ELEMENTAL_STATUS_R", Value: v["terR"]}, {Stat: "DAMAGE_BUFF", Value: v["damage"]}} {
				if s.Value != 0 {
					b.Stats = append(b.Stats, s)
				}
			}
		}
		p.Self = b
		return p, nil
	}
}

// spiritLink (3120006, 3220005) is Java PlayerStats while SPIRIT_LINK is up,
// i.e. while the summon buff is active: HP +x% and damage (and boss damage)
// +damage%. Java also counts the DAMAGE_BUFF statup, so both apply.
func spiritLink(summon int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if !c.Active(summon) {
			return skills.Bonus{}
		}
		return skills.Bonus{MaxHPPercent: c.Values["x"], DamagePercent: c.Values["damage"], BossDamagePercent: c.Values["damage"]}
	}
}
