package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestAcquireLeaseWaitsOutBusyPredecessor(t *testing.T) {
	calls := 0
	epoch, err := AcquireLease(context.Background(), "test", 5*time.Second, func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, domain.ErrLeaseBusy
		}
		return 7, nil
	})
	if err != nil || epoch != 7 || calls != 3 {
		t.Fatalf("epoch=%d calls=%d err=%v", epoch, calls, err)
	}
}

func TestAcquireLeaseGivesUpAfterWait(t *testing.T) {
	calls := 0
	_, err := AcquireLease(context.Background(), "test", 0, func(context.Context) (int, error) {
		calls++
		return 0, domain.ErrLeaseBusy
	})
	if !errors.Is(err, domain.ErrLeaseBusy) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestAcquireLeaseDoesNotRetryOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	_, err := AcquireLease(context.Background(), "test", 5*time.Second, func(context.Context) (int, error) {
		calls++
		return 0, boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
