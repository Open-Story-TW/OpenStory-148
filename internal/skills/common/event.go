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

	// 水晶花園傳送 (Phantom 20031203): moveTo = x, the Phantom's own airship.
	skills.Register(skills.Handler{ID: 20031203, Kind: skills.KindSpecial, Cast: func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		p.MoveTo = c.Value("x")
		return p, nil
	}})
	// 幻影斗蓬: v148 OnKey checks bit 219 and this exact source before it
	// takes an arrow as a cloak direction. The client computes the destination
	// and the WZ direction limit, while the server supplies the short gate.
	skills.Register(skills.Handler{ID: 20031205, Kind: skills.KindSpecial, Cast: buff(0, fixed("PHANTOM_CLOAK", 1))})
	// 幻影技能竊取／技能管理 (Phantom 20031207, 20031208): client-side steal cursor
	// and skill-management UI; server handles peer inspect (0x109), steal/delete
	// (0x108), and choose (0x107) in internal/world/phantom_steal.go.
	for _, id := range []int{20031207, 20031208} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}
	// 卡牌審判 (Phantom 20031209, fourth job 20031210): the world spends the
	// card deck and throws the cards; the drawn card's own bonus (critical
	// rate, drop rate, resistance) is not applied yet.
	for _, id := range []int{20031209, 20031210} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}

	// 萬聖節南瓜: a one-target event attack; the input family is not
	// observed yet, so any is accepted without ammunition.
	skills.Register(skills.Handler{ID: 80001122, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee | combat.Shoot | combat.Magic, Ammo: combat.AmmoNone}})
	skills.Register(skills.Handler{ID: 80001126, Kind: skills.KindHeal | skills.KindPartyBuff, Cast: partyHeal}) // 勝利者的祝福
	// 勝利者的旋風 Victor's Whirlwind: v148 client sends RecvMagicAttack (0x57).
	skills.Register(skills.Handler{ID: 80001125, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Magic}}) // 勝利者的旋風
	skills.Register(skills.Handler{ID: 80001128, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee | combat.Shoot | combat.Magic, Ammo: combat.AmmoNone}})
	// 神龍擺尾: native v148 dispatches 0x57 RecvMagicAttack.
	skills.Register(skills.Handler{ID: 80001130, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee | combat.Magic}})
	// 光之守護 (Knight's Watch link skill 80001140): STANCE = prop for time (matches Mihile 50001214).
	skills.Register(skills.Handler{ID: 80001140, Kind: skills.KindBuff, Cast: buff(0, key("STANCE", "prop"))})
	skills.Register(skills.Handler{ID: 80000023, Kind: skills.KindPassive}) // 寵物達人 Pet Mastery

	// Mr.Dragon: SUMMON 1 for time, a following summon that attacks
	// (Java getSummonMovementType FOLLOW, isMultiAttack).
	skills.Register(skills.Handler{ID: 80001129, Kind: skills.KindSummon, Cast: buff(0, fixed("SUMMON", 1)), Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 1}}})
	// Event stationary puppet summon (v148 0x6AA54D stationary summon table, time=8).
	skills.Register(skills.Handler{ID: 80001141, Kind: skills.KindSummon, Cast: buff(0, fixed("PUPPET", 1)), Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: 0, AssistType: 0}}})
}

// mercedesCase is Java PlayerStats' `case 2001: case 2300..2312:` (Legacy
// lists 2001, not the Mercedes beginner 2002).
func mercedesCase(job int) bool {
	return job == 2001 || job == 2300 || job == 2310 || job == 2311 || job == 2312
}
