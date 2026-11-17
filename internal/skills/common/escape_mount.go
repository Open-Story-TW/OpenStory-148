package common

import (
	"errors"
	"slices"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

const (
	escapeBalloonSkill = 80001065
	escapeFlightSkill  = 80001089
	escapeBalloonItem  = 1992002
)

func init() {
	// TWMS v148 maps the Momo balloon to 80001065 and dispatches Up+Jump
	// while mounted to 80001089. Both native activations are marked disabled
	// in the skill book. These handlers serve learned skills in the two towers.
	skills.Register(skills.Handler{ID: escapeBalloonSkill, Kind: skills.KindBuff | skills.KindMorph,
		AllowDisabledCast: true, Cast: castEscapeBalloon, DropOnEnter: outsideEscapeTowers})
	skills.Register(skills.Handler{ID: escapeFlightSkill, Kind: skills.KindSpecial | skills.KindBuff,
		AllowDisabledCast: true, Cast: castEscapeFlight, DropOnEnter: outsideEscapeTowers})
}

func outsideEscapeTowers(c domain.Character) bool {
	return c.MapID != content.EscapeTower && c.MapID != content.EscapeTraps
}

func castEscapeBalloon(c *skills.CastContext) (skills.CastPlan, error) {
	if outsideEscapeTowers(c.Caster) {
		return skills.CastPlan{}, errors.New("the balloon is only supported in the Escape towers")
	}
	return ride(func(domain.Character) int { return escapeBalloonItem })(c)
}

func castEscapeFlight(c *skills.CastContext) (skills.CastPlan, error) {
	if outsideEscapeTowers(c.Caster) {
		return skills.CastPlan{}, errors.New("mounted flight is only supported in the Escape towers")
	}
	mounted := slices.ContainsFunc(c.Caster.SkillBuffs, func(buff domain.SkillBuff) bool {
		return buff.ID == escapeBalloonSkill && buff.Stat == "MONSTER_RIDING" &&
			buff.Value == escapeBalloonItem && buff.ExpiresAt.After(c.Now)
	})
	if !mounted && !skills.ActiveBuff(c.Caster, escapeFlightSkill, c.Now) {
		return skills.CastPlan{}, errors.New("mounted flight requires the Momo balloon")
	}
	plan := skills.StandardCost(c)
	plan.Toggle = true
	plan.Self = &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "SOARING", Value: c.Value("x")}}}
	return plan, nil
}
