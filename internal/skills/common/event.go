package common

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Event and common skills (job 8000).
func init() {
	// 怪物騎乘 Monster Riding: the equipped taming mob (slot -18) with a
	// saddle (slot -19), Java parseMountInfo.
	skills.Register(skills.Handler{ID: 80001000, Kind: skills.KindBuff | skills.KindMorph, Cast: ride(equippedMount)})

	// 金槍戰鬥機的祝福／光明綠化: Java's indie mapping (ANGEL_ATK/MATK/SPEED/
	// JUMP) for time; the 金槍戰鬥機 ones add VIRTUE_EFFECT 1.
	indie := []stat{key("ANGEL_ATK", "indiePad"), key("ANGEL_MATK", "indieMad"), key("ANGEL_SPEED", "indieSpeed"), key("ANGEL_JUMP", "indieJump")}
	for _, id := range []int{80001034, 80001035, 80001036} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: buff(0, append([]stat{fixed("VIRTUE_EFFECT", 1)}, indie...)...)})
	}
	for _, id := range []int{80001041, 80001042, 80001043} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: buff(0, indie...)})
	}

	// 精靈的祝福 Elven Blessing (80001040, Mercedes 20021110): Java moveTo = x
	// (Elluel). Learned, it also gives EXP +expR%: 80001040 for every job,
	// 20021110 only in Java's PlayerStats case 2001/2300–2312 (Java
	// multiplies expBuff; Bonus.EXP adds).
	for _, id := range []int{80001040, 20021110} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial | skills.KindPassive, Cast: func(c *skills.CastContext) (skills.CastPlan, error) {
			p := skills.StandardCost(c)
			p.MoveTo = c.Value("x")
			return p, nil
		}, Passive: func(c *skills.PassiveContext) skills.Bonus {
			if c.Skill.ID == 20021110 && !mercedesCase(c.Character.Job) {
				return skills.Bonus{}
			}
			return skills.Bonus{EXP: c.Values["expR"]}
		}})
	}

	// 萬聖節南瓜: a one-target event attack; the input family is not
	// observed yet, so any is accepted without ammunition.
	skills.Register(skills.Handler{ID: 80001122, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee | combat.Shoot | combat.Magic, Ammo: combat.AmmoNone}})
	skills.Register(skills.Handler{ID: 80001126, Kind: skills.KindHeal | skills.KindPartyBuff, Cast: partyHeal})           // 勝利者的祝福
	skills.Register(skills.Handler{ID: 80001125, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee}}) // 勝利者的旋風

	// Mr.Dragon: SUMMON 1 for time, a following summon that attacks
	// (Java getSummonMovementType FOLLOW, isMultiAttack).
	skills.Register(skills.Handler{ID: 80001129, Kind: skills.KindSummon, Cast: buff(0, fixed("SUMMON", 1)), Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 1}}})
}

// mercedesCase is Java PlayerStats' `case 2001: case 2300..2312:` (Legacy
// lists 2001, not the Mercedes beginner 2002).
func mercedesCase(job int) bool {
	return job == 2001 || job == 2300 || job == 2310 || job == 2311 || job == 2312
}
