package domain

import "context"

// CombatLootRepository processes durable jobs created in the kill-reward
// transaction. A later world owner can finish a pending job after a crash.
type CombatLootRepository interface {
	PendingCombatLoot(context.Context, WorldLease, int) ([]string, error)
	MaterializeCombatLoot(context.Context, WorldLease, string) ([]ItemDrop, error)
}
