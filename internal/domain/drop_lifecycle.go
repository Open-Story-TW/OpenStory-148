package domain

import "context"

// DropLifecycleRepository expires only this world's unclaimed drops under its
// current lease. Terminal rows remain durable and cannot be picked up again.
type DropLifecycleRepository interface {
	ExpireItemDrops(context.Context, WorldLease, int) ([]ItemDrop, error)
}
