package combat

import "time"

type FieldKind uint8

const (
	FieldArea       FieldKind = iota + 1 // Mist, smoke screen, aura area.
	FieldTownPortal                      // Mystic Door style pair (field + town).
)

// FieldSpec describes a field object a skill places on cast. Registered as
// AttackSpec{Field: &FieldSpec{...}}; the cast path calls the world spawner.
type FieldSpec struct {
	Kind     FieldKind
	AreaType int32         // protocol.AreaMob/AreaPoison/AreaInvincible.
	Duration time.Duration // Zero uses Effect.Time.
	Interval time.Duration // Area tick; zero means one second.
	Box      Box           // Overrides Effect.Box when set.
	// MobStatus is applied each tick to live monsters inside the area,
	// credited to the owner (Poison Mist).
	MobStatus []MobStatusApply
	// PlayersInvincible marks areas whose owner's party ignores monster
	// damage inside (Smoke Screen); queried by the damage path.
	PlayersInvincible bool
	// RecoverMP restores Effect.X% of max MP to every player inside each tick
	// (Recovery Aura 22161003, Java mist type 4); Effect.Prop rolls per player.
	RecoverMP bool
}

// Inside reports whether p lies in the absolute rectangle.
func Inside(lt, rb, p Point) bool {
	return p.X >= lt.X && p.X <= rb.X && p.Y >= lt.Y && p.Y <= rb.Y
}
