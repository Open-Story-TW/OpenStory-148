package common

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Prologue tutorial skills for Aran (2000), Mercedes (2002), Phantom (2003),
// and Demon Slayer (3001), verified against the v148 client skill dispatcher
// (0x93694C), linked-skill resolver (0x6D447A), and movement/passive readers.
func init() {
	// Aran prologue polearm combo attacks (v148 0x93A1BF, melee sender 0x91A08F).
	for _, id := range []int{20000014, 20000015, 20000016} {
		skills.Register(skills.Handler{
			ID:     id,
			Kind:   skills.KindAttack,
			Attack: &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone},
		})
	}
	// Aran prologue combo/body-pressure passives (v148 0x92A80C reads 20000017
	// for prologue combo expiry alongside 21000000).
	for _, id := range []int{20000017, 20000018} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive})
	}

	// Mercedes prologue flight speed passive (v148 0x97F949 reads x=500 locally).
	skills.Register(skills.Handler{ID: 20020111, Kind: skills.KindPassive})
	// Mercedes & Phantom prologue attacks (v148 0x93A2D2 -> ranged/melee, no ammo).
	for _, id := range []int{20021166, 20031166} {
		skills.Register(skills.Handler{
			ID:     id,
			Kind:   skills.KindAttack,
			Attack: &combat.AttackSpec{Kind: combat.Melee | combat.Shoot, Ammo: combat.AmmoNone},
		})
	}
	// Mercedes & Phantom prologue double jumps (v148 0x9461A8 alongside 23001002/24001002).
	for _, id := range []int{20021181, 20031181} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}

	// Demon Slayer prologue Demon Lash 1st hit (v148 0x93A851 -> melee).
	skills.Register(skills.Handler{
		ID:     30010166,
		Kind:   skills.KindAttack,
		Attack: &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone},
	})
	// Demon Slayer prologue Demon Lash follow-ups (v148 0x6D49F4 links 30011167..30011169 to 30010166).
	for _, id := range []int{30011167, 30011168, 30011169} {
		skills.Register(skills.Handler{
			ID:        id,
			Kind:      skills.KindAttack,
			LevelFrom: 30010166,
			Attack:    &combat.AttackSpec{Kind: combat.Melee, Ammo: combat.AmmoNone, LevelFrom: 30010166},
		})
	}
	// Demon Slayer prologue Demon Impact (v148 0x93A651 -> melee, WZ psd=1 psdSkill={30011170} cr=100).
	skills.Register(skills.Handler{
		ID:   30011170,
		Kind: skills.KindAttack,
		Attack: &combat.AttackSpec{
			Kind: combat.Melee,
			Ammo: combat.AmmoNone,
			Prepare: func(ctx *combat.AttackContext) error {
				ctx.Effect.CriticalRate += 100
				return nil
			},
		},
	})
}
