package domain

import "context"

// SceneRepository is mandatory for a world to publish an initial local claim.
// It revalidates a consumed handoff at the publication boundary. It is not a
// replacement for authenticated ownership in individual asset transactions.
type SceneRepository interface {
	ValidateSceneOwner(context.Context, CharacterSession) error
}
