package persistence

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Generic skill receipts historically omitted Wire. The only newly accepted
// nonempty body is native2121003, whose area IDs must be bound to the immutable
// manifest. Ephemeral area ownership remains the world's responsibility.
// The generic ClientTick is a server operation key, not the native a.Tick.
// Keep legacy empty-wire receipts compatible; the live 2121003 path supplies
// this evidence and checks its exact room-area ownership before submission.
func validateMistEruptionWire(q domain.CombatAttackRequest) error {
	if len(q.Wire) == 0 {
		return nil
	}
	if q.SkillID != 2121003 || q.Cost == nil || q.Cost.Opcode != protocol.RecvMagicAttack || q.Cost.Summon != 0 || q.Cost.Ongoing || q.LinesPerTarget < 5 || q.LinesPerTarget > 10 {
		return domain.ErrInvalidInput
	}
	a, err := protocol.ReadAttack(protocol.RecvMagicAttack, protocol.NewReader(q.Wire))
	if err != nil || a.SkillID != q.SkillID || a.LineCount() != q.LinesPerTarget || len(a.Targets) != len(q.Targets) {
		return domain.ErrInvalidInput
	}
	// ApplyCombatAttack sorts its targets; the original wire order is retained.
	targets := make(map[int32]domain.CombatTarget, len(q.Targets))
	for _, target := range q.Targets {
		targets[target.ObjectID] = target
	}
	for _, target := range a.Targets {
		stored, ok := targets[target.ObjectID]
		if !ok || len(stored.Lines) != len(target.Lines) {
			return domain.ErrInvalidInput
		}
		for i, line := range target.Lines {
			if line.Raw != stored.Lines[i].Raw {
				return domain.ErrInvalidInput
			}
		}
	}
	return nil
}
