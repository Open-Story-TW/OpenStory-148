package skills

import (
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"time"
)

// PartyCast resolves a party skill for its caster: the skill must be learned
// at exactly level with its prerequisites, and the registered Cast must return
// a Party buff with a mirror-symmetric Area (caster facing is not tracked, so
// an asymmetric range is refused rather than guessed).
func PartyCast(root string, actor domain.Character, skillID, level int, now time.Time) (CastPlan, error) {
	h, ok := Lookup(skillID)
	if !ok || h.Cast == nil || h.Kind&KindPartyBuff == 0 {
		return CastPlan{}, fmt.Errorf("skill%d: not a registered party skill", skillID)
	}
	active := func(id int) int {
		for _, s := range actor.Skills {
			if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
				return s.Level
			}
		}
		return 0
	}
	levelID := skillID
	if h.LevelFrom > 0 {
		levelID = h.LevelFrom
	}
	level, ok = CastLevel(actor, levelID, skillID, level, now)
	if !ok {
		return CastPlan{}, fmt.Errorf("skill%d: level %d not learned", skillID, level)
	}
	template, e := content.SkillDefinition(root, skillID)
	stolen := actor.ChosenStolenLevel(skillID, now) > 0
	if e != nil || template.Disabled || (!template.CanLearnBy(actor) && !stolen) || actor.Level < template.ReqLevel {
		return CastPlan{}, fmt.Errorf("skill%d: unavailable for job %d", skillID, actor.Job)
	}
	if !stolen {
		for id, need := range template.Requirements {
			if active(id) < need {
				return CastPlan{}, fmt.Errorf("skill%d: prerequisite %d", skillID, id)
			}
		}
	}
	values, e := template.Values(level)
	if e != nil {
		return CastPlan{}, e
	}
	p, e := h.Cast(&CastContext{Skill: template, Level: level, Values: values, Caster: actor, Now: now})
	if e != nil {
		return CastPlan{}, e
	}
	ScaleBuffDuration(actor, p.Party, now)
	if p.Party != nil && p.Area == nil {
		if r, ok := template.RangeAt(level); ok {
			p.Area = &Rect{Left: r.LT[0], Top: r.LT[1], Right: r.RB[0], Bottom: r.RB[1]}
		}
	}
	if p.Self != nil || p.Party == nil || p.Area == nil || p.Area.Left != -p.Area.Right || p.Area.Left > 0 || p.Area.Top > p.Area.Bottom || p.MPCost < 0 || p.HPCost < 0 || p.Cooldown < 0 || p.Cooldown > 86400*time.Second {
		return CastPlan{}, fmt.Errorf("skill%d: unsupported party plan", skillID)
	}
	return p, nil
}

// InPartyArea reports whether target is a living recipient inside area
// around source.
func InPartyArea(area Rect, source, target domain.Character) bool {
	return target.HP > 0 && InPartyRange(area, source, target)
}

// InPartyRange reports whether target stands inside area around source.
func InPartyRange(area Rect, source, target domain.Character) bool {
	dx, dy := int64(target.X)-int64(source.X), int64(target.Y)-int64(source.Y)
	return dx >= int64(area.Left) && dx <= int64(area.Right) && dy >= int64(area.Top) && dy <= int64(area.Bottom)
}

// PartyRecipient reports whether a party member (not the caster) receives a
// party plan: living members normally, dead members for a Revive plan.
func PartyRecipient(plan CastPlan, source, target domain.Character) bool {
	return plan.Area != nil && plan.Revive == (target.HP <= 0) && InPartyRange(*plan.Area, source, target)
}

// MPCost is checked: unresolved source data must not look like a free cast.
func MPCost(c domain.Character, skillID, cost int, now time.Time) (int, error) {
	p, err := ResolveResourceCost(c, skillID, cost, 0, now)
	return p.MP, err
}
