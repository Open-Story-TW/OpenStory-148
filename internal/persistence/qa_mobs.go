package persistence

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"time"
)

// Called only after the normal SQL session, character and grant locks. The
// receipt audits authorization; the world separately records the actual IDs.
func authorizeQAMobs(c domain.Character, r domain.GMCommandRequest, grant domain.QAGMGrant, now time.Time) (*domain.QAMobAuthorization, error) {
	if !domain.ValidQAMobCommand(r.Name, r.Args, c.MapID) || c.HP <= 0 || len(r.RuntimeID) != 32 {
		return nil, domain.ErrInvalidInput
	}
	for _, b := range r.RuntimeID {
		if !(b >= 'a' && b <= 'f' || b >= '0' && b <= '9') {
			return nil, domain.ErrInvalidInput
		}
	}
	if r.Name == "spawn" {
		catalog, err := content.DefaultCatalog()
		if err != nil {
			return nil, err
		}
		mob, err := catalog.Mob(int(r.Args[0]))
		if err != nil || mob.HP <= 0 {
			return nil, domain.ErrInvalidInput
		}
		body, err := catalog.MobBodyBounds(mob.ID)
		if err != nil || !body.Envelope.Nonempty() || body.FrameCount < 1 {
			return nil, domain.ErrInvalidInput
		}
	}
	expires := now.Add(domain.QAMobLifetime)
	if grant.ExpiresAt.Before(expires) {
		expires = grant.ExpiresAt
	}
	if !expires.After(now) {
		return nil, domain.ErrForbidden
	}
	return &domain.QAMobAuthorization{RuntimeID: r.RuntimeID, ExpiresAt: expires, GrantRevision: grant.Revision}, nil
}
