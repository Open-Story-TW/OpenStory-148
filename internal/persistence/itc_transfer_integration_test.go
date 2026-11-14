package persistence

import (
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

func TestITCTransferOpensTheTradingCenterOnce(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	lease := cashTestLease(t, ctx, s, a.CharacterID)

	handle := a.CharacterID + 200000
	if _, err := s.BeginITCTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil || !cs.ITC || cs.NativeHandle != handle {
		t.Fatalf("itc claim=%+v %v", cs, err)
	}
	if _, err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("double claim=%v", err)
	}
	if err = s.ReleaseCashCharacter(ctx, cs); err != nil {
		t.Fatal(err)
	}

	// A plain cash shop ticket still opens the shop.
	handle = b.CharacterID + 200000
	if _, err = s.BeginCashTransfer(ctx, b, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil || cs.ITC {
		t.Fatalf("cash claim=%+v %v", cs, err)
	}
	if err = s.ReleaseCashCharacter(ctx, cs); err != nil {
		t.Fatal(err)
	}
}
