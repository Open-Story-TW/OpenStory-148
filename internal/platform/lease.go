package platform

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"maple-go-server/internal/domain"
)

// LeaseWait bounds how long a restarted process waits for a crashed
// predecessor's lease (20s TTL) to expire before giving up.
const LeaseWait = 35 * time.Second

// AcquireLease retries claim while another instance still holds a live lease,
// so a restart right after a crash waits out the TTL instead of exiting.
func AcquireLease[T any](ctx context.Context, service string, wait time.Duration, claim func(context.Context) (T, error)) (T, error) {
	deadline := time.Now().Add(wait)
	for attempt := 1; ; attempt++ {
		lease, err := claim(ctx)
		if !errors.Is(err, domain.ErrLeaseBusy) || !time.Now().Before(deadline) {
			return lease, err
		}
		if attempt == 1 {
			slog.Info("lease_wait", "service", service, "max_wait", wait.String())
		}
		select {
		case <-ctx.Done():
			return lease, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
