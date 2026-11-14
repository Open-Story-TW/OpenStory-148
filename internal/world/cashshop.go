package world

import (
	"context"

	"maple-go-server/internal/domain"
)

// prepareCashshop hands the character to the cash shop process. With itc
// set the ticket opens the item trading center (拍賣) instead of the shop.
func (s *Server) prepareCashshop(ctx context.Context, live *liveCharacter, ip string, itc bool) error {
	repo, ok := s.repo.(domain.CashRepository)
	if !ok {
		return domain.ErrForbidden
	}
	begin := repo.BeginCashTransfer
	if itc {
		itcRepo, ok := s.repo.(domain.ITCTransferRepository)
		if !ok {
			return domain.ErrForbidden
		}
		begin = itcRepo.BeginITCTransfer
	}
	// Cross-process transfer needs a durable snapshot; same-world channel
	// transfers deliberately keep the existing in-memory aggregate instead.
	if err := s.checkpoint(ctx, live); err != nil {
		return err
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	handle := live.nativeHandle
	live.mu.Unlock()
	_, err := begin(ctx, session, handle, ip)
	return err
}
