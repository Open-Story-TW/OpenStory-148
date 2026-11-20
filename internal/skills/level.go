package skills

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"time"
)

// EffectiveLevel adds active COMBAT_ORDERS to learned levels within the WZ
// limits. Native86CF1C excludes beginner skills and Combat Orders1211011 itself.
// Missing/expired skills return zero. Potential increments and the remaining
// native special getter branches require their own complete source policy.
func EffectiveLevel(c domain.Character, id int, now time.Time) int {
	return levelFor(c, id, id, now)
}

// EquipmentRelatedSkillLevel authorizes only the original ring summon's
// hidden assistance effect. The public cast path still requires its own level.
func EquipmentRelatedSkillLevel(c domain.Character, id int, now time.Time) int {
	switch id % 10000 {
	case 86, 88, 91, 180:
	default:
		return 0
	}
	parent := id + 999
	h, ok := Lookup(parent)
	if !ok || h.CastEquipment == 0 || h.Attack == nil || h.Attack.Summon == nil || h.Attack.Summon.AssistType != 2 || h.Attack.Summon.Sub == nil {
		return 0
	}
	level, ok := CastLevel(c, parent, parent, 1, now)
	if !ok {
		return 0
	}
	return level
}

// CastLevel validates a client-sent level for skill id, whose level is
// learned through learnedID (Handler.LevelFrom, else id). The client sends
// the effective level; the plain learned level is also accepted while a
// COMBAT_ORDERS change is in flight. It returns the level to use.
func CastLevel(c domain.Character, learnedID, id, client int, now time.Time) (int, bool) {
	if h, ok := Lookup(id); ok && h.CastEquipment != 0 {
		if client == 1 {
			for _, item := range c.Equipment {
				// Original v148 0x4D926A scans the 35 ordinary equip slots.
				if item.ItemID == h.CastEquipment && item.Slot < 0 && item.Slot >= -35 &&
					(item.ExpiresAt <= 0 || item.ExpiresAt > now.UnixMilli()) {
					return 1, true
				}
			}
		}
		return 0, false
	}
	base := learnedLevel(c, learnedID, now)
	if base < 1 || client < 1 {
		return 0, false
	}
	if client == levelFor(c, learnedID, id, now) {
		return client, true
	}
	if client != base {
		return 0, false
	}
	if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id); err == nil {
		return min(base, def.MaxLevel), true // A linked skill may cap below its parent.
	}
	return base, true
}

// CombatOrders returns the active COMBAT_ORDERS buff value of c.
func CombatOrders(c domain.Character, now time.Time) int {
	for _, b := range c.SkillBuffs {
		if b.Stat == "COMBAT_ORDERS" && b.ExpiresAt.After(now) {
			return max(0, b.Value)
		}
	}
	return 0
}

func learnedLevel(c domain.Character, id int, now time.Time) int {
	for _, s := range c.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	if DojoSkill(id) && id/10000 == domain.BeginnerBook(c.Job) {
		// The v148 skill level getter (0x6D447A) gives suffixes 1009-1011
		// of the character's own beginner book their maximum level, 1,
		// without a learned record.
		return 1
	}
	return 0
}

// DojoSkill reports the Mu Lung Dojo energy skills of every beginner book:
// x1009 竹竿千擊, x1010 金剛不壞 and x1011 地火天爆.
func DojoSkill(id int) bool {
	switch id % 10000 {
	case 1009, 1010, 1011:
		book := id / 10000
		return domain.IsBeginnerJob(book) && domain.BeginnerBook(book) == book
	}
	return false
}

func levelFor(c domain.Character, learnedID, id int, now time.Time) int {
	base := learnedLevel(c, learnedID, now)
	if base < 1 {
		return 0
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil {
		return base
	}
	bonus := 0
	if id != 1211011 && !domain.IsBeginnerJob(id/10000) {
		bonus = CombatOrders(c, now)
	}
	if bonus == 0 {
		return min(base, def.MaxLevel)
	}
	return min(base+bonus, def.TrueMax())
}
