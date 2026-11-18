package domain

import "context"

// MarriageRepository recognizes a registered marriage with both partners.
type MarriageRepository interface {
	CharacterMarried(context.Context, CharacterSession) (bool, error)
}
