package combat

import (
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

var (
	ErrKind     = errors.New("attack family not allowed for skill")
	ErrCount    = errors.New("attack target or line count exceeds skill")
	ErrRange    = errors.New("attack target outside skill range")
	ErrCost     = errors.New("attack cost not payable")
	ErrRejected = errors.New("attack rejected by skill rule")
)

// TargetInfo is the room's authoritative view of one claimed target.
type TargetInfo struct {
	ObjectID   int32
	Mob        content.Mob
	X, Y       int
	HP         int64
	Statuses   MobStatuses
	HalfWidth  int // Body half extents for range admission (0 = point).
	BodyHeight int
}

// AttackContext is the validated request a skill rule may inspect.
type AttackContext struct {
	Kind      AttackKind
	Character domain.Character
	Stats     PlayerStats
	Effect    Effect
	Attack    protocol.Attack
	Targets   []TargetInfo
	Now       time.Time
	// AF-11's physical and magic field multipliers come from the owner-bound
	// affected area around the character in the current gameplay scene.
	FieldPhysicalAmp, FieldMagicAmp int
	// NoAmmo may be set by Prepare (Shadow Stars / Soul Arrow active).
	NoAmmo bool
	// Mesos are the consumed ground meso amounts (ConsumeMesos skills).
	Mesos []int64
	// SummonSkillID is the attacking summon's shown skill: the variant for
	// SummonSpec.Variants (Elemental Knights 23111008/9/10), else the cast skill.
	SummonSkillID int
}

// Cost is paid in the same settlement transaction as the damage.
type Cost struct {
	MP, HP, Money int
	Bullets       int
	Ammo          Ammo
	// ItemID/Items is a fixed consumable (Java itemCon/itemConNo, e.g.
	// Flamethrower capsules) taken from any USE stack.
	ItemID, Items int
}

type TargetPlan struct {
	ObjectID    int32
	Lines       []int                      // Applied per-line damage after ceilings (before HP clip).
	Criticality []domain.CombatCriticality // Only populated for reviewed ambiguous wire families.
	Statuses    []ActiveStatus
	// BossCriticalStep is the physical boss bonus already included in the first hit's rate.
	BossCriticalStep int
	// CriticalRate is the first connected hit's rate for this target (skill, boss and stun bonuses).
	CriticalRate int
	// Critical marks lines above the noncritical bound: the wire does not say,
	// but only a critical reaches there. Other clients need it to draw the number.
	Critical []bool
	// Normal is each line's noncritical bound, for lines the critical audit refuses.
	Normal []int
	// Clipped counts lines the client claimed above the bound; BelowFloor
	// counts non-zero lines under the mastery floor (a client roll cannot
	// produce them).
	Clipped, BelowFloor int
}

func (t TargetPlan) LineCriticalRate(firstRate, connectedIndex int) int {
	if t.BossCriticalStep == 0 {
		return firstRate
	}
	return min(100-criticalAuditBase, firstRate+connectedIndex*t.BossCriticalStep)
}

func (t TargetPlan) Total() int64 {
	var n int64
	for _, l := range t.Lines {
		n += int64(l)
	}
	return n
}

type Plan struct {
	Cost     Cost
	Heal     int // HP regained by Drain.
	MPGain   int // MP regained by Drain.
	Cooldown time.Duration
	Targets  []TargetPlan
}

// Default admission radii when a skill has no lt/rb box.
const (
	defaultMeleeRange = 220
	defaultShootRange = 600
	defaultMagicRange = 400
	// Summons pick targets from their own attack box around the summon.
	defaultSummonRange   = 500
	summonStatusDuration = 4 * time.Second
	rangeTolerance       = 30
	// rangeSlack widens the horizontal reach (skill range plus the mob's half
	// body): a mob keeps walking between its controller's movement reports, so
	// the room's position trails the one the attacking client aimed at.
	rangeSlack    = 2
	verticalRange = 160
)

func admits(ctx AttackContext, t TargetInfo) bool {
	origin := Point{int(ctx.Attack.X), int(ctx.Attack.Y)}
	if ctx.Effect.Box.Set {
		// Either mirror is accepted: the facing bit is not reliable for every
		// skill packet (the same tolerance as world mobCastReaches).
		pad := t.HalfWidth * rangeSlack
		for _, left := range []bool{ctx.Attack.FacingLeft(), !ctx.Attack.FacingLeft()} {
			lt, rb := ctx.Effect.Box.At(origin, left)
			// Only outwards: a box that starts away from the caster keeps its near edge.
			lt.X = min(lt.X, origin.X+(lt.X-origin.X)*rangeSlack)
			rb.X = max(rb.X, origin.X+(rb.X-origin.X)*rangeSlack)
			if t.X >= lt.X-pad && t.X <= rb.X+pad && t.Y >= lt.Y-rangeTolerance && t.Y-t.BodyHeight <= rb.Y+rangeTolerance {
				return true
			}
		}
		if ctx.Kind != Shoot && ctx.Kind != Magic {
			return false
		}
		// A projectile flies up to range and lt/rb is the blast around where it
		// lands (Arrow Blow: range 420, lt/rb +-120), so the blast extends the reach.
		_, rb := ctx.Effect.Box.At(origin, false)
		return withinRange(ctx, t, origin, max(0, rb.X-origin.X))
	}
	return withinRange(ctx, t, origin, 0)
}

func withinRange(ctx AttackContext, t TargetInfo, origin Point, extra int) bool {
	r := ctx.Effect.Range
	if r <= 0 {
		switch ctx.Kind {
		case Shoot:
			r = defaultShootRange
		case Magic:
			r = defaultMagicRange
		case Summoned:
			r = defaultSummonRange
		default:
			r = defaultMeleeRange
		}
	}
	dx, dy := t.X-origin.X, t.Y-origin.Y
	return max(dx, -dx) <= (r+extra+t.HalfWidth)*rangeSlack && max(dy, -dy) <= verticalRange+t.BodyHeight
}

// PlanAttack validates counts, range, ceilings and cost and resolves on-hit
// statuses. It never trusts client damage above the server ceiling.
func PlanAttack(spec *AttackSpec, ctx AttackContext, roll func(int) bool) (Plan, error) {
	var p Plan
	if !spec.Allows(ctx.Kind) {
		return p, ErrKind
	}
	if err := spec.ApplyLearnedEffect(ctx.Character, ctx.Now, &ctx.Effect); err != nil {
		return p, err
	}
	if spec != nil && spec.Prepare != nil {
		if err := spec.Prepare(&ctx); err != nil {
			return p, err
		}
	}
	e := ctx.Effect
	lines := e.Lines(ctx.Kind)
	if ctx.Stats.ShadowPartner > 0 && (ctx.Kind == Shoot || ctx.Kind == Melee) {
		lines *= 2
	}
	if len(ctx.Attack.Targets) != len(ctx.Targets) || len(ctx.Targets) > e.MobCount || ctx.Attack.LineCount() > lines {
		return p, fmt.Errorf("%w: targets %d/%d, lines %d/%d (shadow partner %d)", ErrCount, len(ctx.Targets), e.MobCount, ctx.Attack.LineCount(), lines, ctx.Stats.ShadowPartner)
	}
	resources, err := PreparedResourceCost(spec, ctx)
	if err != nil {
		return p, err
	}
	p.Cost = Cost{MP: resources.MP, HP: e.HPCon, Money: e.MoneyCon}
	if e.HPRatio > 0 {
		p.Cost.HP += ctx.Character.HP * e.HPRatio / 100
	}
	if e.ItemCon > 0 {
		p.Cost.ItemID, p.Cost.Items = e.ItemCon, max(1, e.ItemConNo)
	}
	if ctx.Kind == Shoot && !ctx.NoAmmo {
		ammo := AmmoNone
		if spec != nil && spec.Ammo != AmmoAuto {
			ammo = spec.Ammo
		} else if w := WeaponFor(ctx.Stats.WeaponID); w.Projectile != AmmoAuto {
			ammo = w.Projectile
		}
		if ammo != AmmoNone {
			p.Cost.Ammo = ammo
			p.Cost.Bullets = max(1, e.BulletCount)
			if e.BulletConsume > 0 {
				p.Cost.Bullets = e.BulletConsume
			}
		}
	}
	if ctx.Kind == Summoned {
		p.Cost = Cost{}
	}
	if ctx.Character.MP < p.Cost.MP || p.Cost.HP > 0 && ctx.Character.HP <= p.Cost.HP {
		return p, ErrCost
	}
	p.Cooldown = resources.Cooldown
	// Some skills send a melee or body packet but use the magic formula.
	magic := ctx.Kind == Magic || spec != nil && (spec.MagicDamage || ctx.Kind == Body && spec.Kind&Magic != 0)
	fieldAmp := ctx.FieldPhysicalAmp
	if magic {
		fieldAmp = ctx.FieldMagicAmp
	}
	maxBase := MaxBaseDamage(ctx.Stats)
	for i, t := range ctx.Targets {
		in := ctx.Attack.Targets[i]
		if in.ObjectID != t.ObjectID || t.HP <= 0 {
			return p, ErrRejected
		}
		if (spec == nil || !spec.IgnoreRange) && !admits(ctx, t) {
			return p, fmt.Errorf("%w: target %d at (%d,%d) body %dx%d, origin (%d,%d) left %v, box %+v range %d", ErrRange, t.ObjectID, t.X, t.Y, t.HalfWidth, t.BodyHeight, ctx.Attack.X, ctx.Attack.Y, ctx.Attack.FacingLeft(), ctx.Effect.Box, ctx.Effect.Range)
		}
		dc := DamageContext{Stats: ctx.Stats, Effect: e, Mob: t.Mob, Statuses: t.Statuses, Magic: magic, HP: t.HP, Attacker: ctx.Character, FieldDamagePercent: fieldAmp}
		floor, ceiling := 0, 0
		if spec != nil && spec.MaxDamage != nil {
			ceiling = spec.MaxDamage(dc)
		} else if spec != nil && spec.DamageRange != nil {
			low, high := dc, dc
			low.Effect.Damage, high.Effect.Damage = spec.DamageRange(e)
			floor, _ = LineBounds(low)
			_, ceiling = LineBounds(high)
		} else if ctx.Kind == Summoned {
			ceiling = SummonCeiling(dc)
		} else {
			floor, ceiling = LineBounds(dc)
		}
		// A skill's or passive's own ceiling (Meso Explosion, Mortal Blow) is the exact bound.
		ownCeiling := spec != nil && spec.MaxDamage != nil
		if modified := applyCeilingModifiers(dc, ceiling); modified != ceiling {
			// A passive rewrote the bound (Mortal Blow, Imprint): the mastery
			// floor no longer describes this line.
			ceiling, floor, ownCeiling = modified, 0, true
		}
		tp := TargetPlan{ObjectID: t.ObjectID, CriticalRate: ctx.Stats.CriticalRateAgainst(e.CriticalRate, t.Mob.Boss)}
		if stun := t.Statuses[MobStun]; stun != nil && stun.ExpiresAt.After(ctx.Now) {
			tp.CriticalRate += ctx.Stats.StunnedCritRate
		}
		if t.Mob.Boss && (ctx.Kind == Melee || ctx.Kind == Shoot) {
			// The native physical calculation adds the boss rate on each connected line.
			tp.BossCriticalStep = max(0, ctx.Stats.BossCritRate)
		}
		landed := false
		base := e.Lines(ctx.Kind)
		for j, l := range in.Lines {
			f, c := max(0, floor), max(0, ceiling)
			if !ownCeiling {
				f, c = WireLineBounds(dc, ctx.Kind, l.Raw, floor, max(0, ceiling))
			}
			normal := max(0, ceiling)
			tp.Criticality = append(tp.Criticality, ReviewedWireCriticality(ctx.Kind, e.SkillID, l.Raw))
			if j >= base && ctx.Stats.ShadowPartner > 0 {
				// Shadow Partner's mirrored line set deals x% of the original.
				c = c * min(ctx.Stats.ShadowPartner, 100) / 100
				f = f * min(ctx.Stats.ShadowPartner, 100) / 100
				normal = normal * min(ctx.Stats.ShadowPartner, 100) / 100
			}
			claimed := l.Claimed()
			if InstantKillLine(e.SkillID, j, t.Mob, int64(claimed)) {
				// The client's instant kill is the target's maximum HP, not a roll.
				f, c, normal = claimed, claimed, claimed
			}
			v := min(claimed, c)
			tp.Critical = append(tp.Critical, !ownCeiling && !l.Critical() && UnmarkedMayBeCritical(dc, ctx.Kind) && claimed > normal)
			tp.Normal = append(tp.Normal, normal)
			if claimed > c {
				tp.Clipped++
			}
			if claimed > 0 && claimed < f {
				// A miss is 0; anything else under the mastery floor is
				// not a client roll. Recorded, not rejected (see attack_applied).
				tp.BelowFloor++
			}
			tp.Lines = append(tp.Lines, v)
			landed = landed || v > 0
		}
		if landed {
			applies := append(append([]MobStatusApply(nil), e.MonsterStatus...), specOnHit(spec)...)
			for _, a := range applies {
				if !CanInflict(t.Mob, a.Stat, e.Element) {
					continue
				}
				if ctx.Kind == Summoned && a.Duration == 0 && !a.SubTime && !a.Poison {
					// Java SummonHandler applies summon statuses for 4000ms; the
					// level's time is the summon's own lifetime.
					a.Duration = summonStatusDuration
				}
				if st, ok := ResolveOn(a, e, ctx.Character.ID, maxBase, t.Mob, ctx.Now, roll); ok {
					tp.Statuses = append(tp.Statuses, st)
				}
			}
		}
		p.Targets = append(p.Targets, tp)
	}
	if spec != nil && spec.Validate != nil {
		if err := spec.Validate(&ctx); err != nil {
			return p, err
		}
	}
	if drains := drainers(spec); len(drains) > 0 {
		var dealt int64
		for i, t := range p.Targets {
			lines := append([]int(nil), t.Lines...)
			dealt += ClipToHP(lines, ctx.Targets[i].HP)
		}
		hp, mp := 0, 0
		for _, d := range drains {
			h, m := d(&ctx, dealt)
			hp, mp = hp+h, mp+m
		}
		p.Heal = int(max(0, min(int64(hp), dealt, int64(ctx.Character.MaxHP-ctx.Character.HP+p.Cost.HP))))
		p.MPGain = max(0, min(mp, ctx.Character.MaxMP-ctx.Character.MP+p.Cost.MP))
	}
	return p, nil
}

func specOnHit(s *AttackSpec) []MobStatusApply {
	if s == nil {
		return nil
	}
	return s.OnHit
}

// ClipToHP reduces applied lines from the end so the total never exceeds hp;
// it returns the effective total.
func ClipToHP(lines []int, hp int64) int64 {
	var total int64
	for i, l := range lines {
		if total+int64(l) > hp {
			lines[i] = int(max(0, hp-total))
		}
		total += int64(lines[i])
	}
	return total
}
