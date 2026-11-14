package skills

import (
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// AttackResourceCost reconstructs cost-only policy from fresh character/WZ
// state. General Prepare/on-hit/RNG callbacks never run. Opcode zero is retained
// for old internal callers; network plans always bind the actual opcode.
func AttackResourceCost(c domain.Character, id, level int, opcode uint16, now time.Time) (content.ResourceCost, bool, error) {
	spec, effect := combat.NormalAttack(), combat.NormalEffectFor(c)
	if id != 0 {
		h, ok := Lookup(id)
		if !ok || h.Attack == nil {
			return content.ResourceCost{}, false, fmt.Errorf("attack cost spec unavailable")
		}
		spec = h.Attack
		def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
		if err != nil {
			return content.ResourceCost{}, false, err
		}
		if !def.CanLearnBy(c) {
			return content.ResourceCost{}, false, fmt.Errorf("attack cost wrong job")
		}
		if spec.Effect.SkillID != 0 {
			effect = spec.Effect
			effect.Level = level
		} else {
			if effect, err = combat.FromSkill(def, level); err != nil {
				return content.ResourceCost{}, false, err
			}
		}
	}
	if opcode == 0 {
		opcode = protocol.RecvMeleeAttack
		if spec != nil {
			for _, op := range []uint16{protocol.RecvMeleeAttack, protocol.RecvShootAttack, protocol.RecvMagicAttack, protocol.RecvBodyAttack} {
				if spec.Allows(combat.KindForOpcode(op)) {
					opcode = op
					break
				}
			}
		}
	}
	kind := combat.KindForOpcode(opcode)
	if kind == 0 || kind == combat.Summoned || !spec.Allows(kind) || id == 0 && kind != combat.Melee && kind != combat.Shoot {
		return content.ResourceCost{}, false, fmt.Errorf("attack resource opcode mismatch")
	}
	modifiers, err := ResourceModifiers(c, now)
	if err != nil {
		return content.ResourceCost{}, false, err
	}
	ctx := combat.AttackContext{Character: c, Kind: kind, Effect: effect, Stats: combat.PlayerStats{ResourceCosts: modifiers}, Now: now,
		Attack: protocol.Attack{Opcode: opcode, SkillID: id}}
	out, err := combat.PrepareResourceCost(spec, ctx)
	if err != nil {
		return content.ResourceCost{}, false, err
	}
	ongoing := combat.OngoingResourceCast(ctx)
	out.Cooldown = combat.ResourceCooldown(out, ongoing)
	return out, ongoing, nil
}
