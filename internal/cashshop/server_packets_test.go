package cashshop

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestPurchaseFailuresDoNotMislabelInvalidActionsAsInsufficient(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code byte
	}{
		{domain.ErrInsufficient, 3},
		{errors.Join(domain.ErrInsufficient, domain.ErrCashLockerFull), 10},
		{errors.Join(domain.ErrCashGender, domain.ErrForbidden), 21},
		{domain.ErrForbidden, 12},
		{domain.ErrNotFound, 12},
		{domain.ErrInvalidInput, 12},
		{errors.New("database unavailable"), 12},
	} {
		if got := cashPurchaseFailure(tc.err); got != tc.code {
			t.Fatalf("%v reported as code%d, want%d", tc.err, got, tc.code)
		}
	}
}

// Reasons follow the v145 handler 0x47BF4C StringPool texts, not Legacy Java.
func TestGiftAndPackageFailuresUseNativeNotices(t *testing.T) {
	gender := errors.Join(domain.ErrCashGender, domain.ErrForbidden)
	for _, tc := range []struct {
		kind string
		err  error
		code byte
	}{
		{"gift", errors.Join(domain.ErrCashSameAccount, domain.ErrForbidden), 6},
		{"gift", domain.ErrNotFound, 7},
		{"gift", gender, 8},
		{"gift_package", gender, 8},
		{"package", gender, 21},
		{"gift", domain.ErrBadCredentials, 32},
		{"gift", domain.ErrInsufficient, 3},
		{"package", errors.Join(domain.ErrCashLockerFull, domain.ErrInsufficient), 10},
		{"gift", domain.ErrInvalidInput, 11},
		{"package", domain.ErrInvalidInput, 12},
		{"coupon", domain.ErrAlreadyExists, 16},
		{"coupon", domain.ErrNotFound, 14},
	} {
		if got := cashFeatureFailure(tc.kind, tc.err); got != tc.code {
			t.Fatalf("%s %v reported as code%d, want%d", tc.kind, tc.err, got, tc.code)
		}
	}
	if got := cashMoveFailure(false, domain.ErrInsufficient); got != 24 {
		t.Fatalf("locker->inventory full=%d", got)
	}
	if got := cashMoveFailure(true, errors.Join(domain.ErrCashLockerFull, domain.ErrInsufficient)); got != 10 {
		t.Fatalf("inventory->locker full=%d", got)
	}
}

type packetCashRepo struct{ domain.CashRepository }

func (packetCashRepo) ConsumeCashTransfer(context.Context, int64, string, domain.ServiceLease) (domain.CashSession, error) {
	return domain.CashSession{AccountName: "PacketQA", Session: domain.CharacterSession{CharacterID: 1, AccountID: 1, SessionID: "cash-packet-qa", Generation: 1, Character: domain.Character{ID: 1, Name: "PacketQA", Level: 1, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5}}}, nil
}
func (packetCashRepo) ReadCashWallet(context.Context, domain.CashSession) (domain.CashWallet, error) {
	return domain.CashWallet{}, nil
}
func (packetCashRepo) ReadCashLocker(context.Context, domain.CashSession) ([]domain.CashItem, error) {
	return nil, nil
}
func (packetCashRepo) ReleaseCashCharacter(context.Context, domain.CashSession) error { return nil }
func (packetCashRepo) ReadCashPurchaseRecord(_ context.Context, _ domain.CashSession, sn int) (bool, error) {
	return sn == 10000000, nil
}

func TestUnrelatedCashEntryPacketDoesNotReportInsufficientFunds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s := &server{repo: packetCashRepo{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if raw, err := listener.Accept(); err == nil {
			s.handle(ctx, raw)
		}
	}()
	c, err := protocol.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetTimeouts(time.Second, time.Second)
	login := protocol.NewWriter(protocol.RecvLoggedIn)
	login.I32(123)
	if err = c.WritePacket(login.Packet()); err != nil {
		t.Fatal(err)
	}
	for range 6 { // open, catalogue, gifts, locker, wallet, cash access.
		if _, err := c.ReadPacket(); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.WritePacket(protocol.NewWriter(0x7ff0).Packet()); err != nil {
		t.Fatal(err)
	}
	if err = c.WritePacket(protocol.NewWriter(protocol.RecvCashUpdate).Packet()); err != nil {
		t.Fatal(err)
	}
	for range 4 { // gifts, locker, wallet, cash access; no extra failure packet.
		packet, err := c.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(packet, protocol.CashFailure(3)) {
			t.Fatal("a non-purchase packet invented an insufficient-funds notice")
		}
	}
	for _, sn := range []int32{0, 10000000, 12345678} {
		query := protocol.NewWriter(protocol.RecvCashOperation)
		query.U8(46)
		query.I32(sn)
		if err = c.WritePacket(query.Packet()); err != nil {
			t.Fatal(err)
		}
		packet, err := c.ReadPacket()
		if err != nil || !bytes.Equal(packet, protocol.CashPurchaseRecord(int(sn), sn == 10000000)) {
			t.Fatalf("initial catalogue query produced a financial failure: %x %v", packet, err)
		}
	}
	unsupported := protocol.NewWriter(protocol.RecvCashOperation)
	unsupported.U8(255)
	if err = c.WritePacket(unsupported.Packet()); err != nil {
		t.Fatal(err)
	}
	packet, err := c.ReadPacket()
	if err != nil || !bytes.Equal(packet, protocol.CashFailure(12)) {
		t.Fatalf("unsupported action should be invalid request, got %x %v", packet, err)
	}
	c.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("cash packet test cleanup stalled")
	}
}

type packetCouponRepo struct {
	packetCashRepo
	requests chan domain.CashFeatureRequest
}

func (packetCouponRepo) ReadCashWallet(context.Context, domain.CashSession) (domain.CashWallet, error) {
	return domain.CashWallet{Credit: 943}, nil
}
func (packetCouponRepo) ReadCashWishlist(context.Context, domain.CashSession) ([10]int, error) {
	return [10]int{}, nil
}
func (packetCouponRepo) ClaimCashGifts(context.Context, domain.CashSession) ([]domain.CashGift, error) {
	return nil, nil
}
func (r packetCouponRepo) ChangeCashFeature(_ context.Context, request domain.CashFeatureRequest) (domain.CashFeatureResult, error) {
	r.requests <- request
	if request.Coupon == "used-test" {
		return domain.CashFeatureResult{}, errors.Join(errors.New("coupon rejected"), domain.ErrAlreadyExists)
	}
	return domain.CashFeatureResult{}, domain.ErrNotFound
}

func TestNativeCouponFailureUsesSerialNoticeAndPreservesWallet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	repo := packetCouponRepo{requests: make(chan domain.CashFeatureRequest, 2)}
	s := &server{repo: repo}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if raw, err := listener.Accept(); err == nil {
			s.handle(ctx, raw)
		}
	}()
	c, err := protocol.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetTimeouts(time.Second, time.Second)
	login := protocol.NewWriter(protocol.RecvLoggedIn)
	login.I32(123)
	if err = c.WritePacket(login.Packet()); err != nil {
		t.Fatal(err)
	}
	for range 7 {
		if _, err = c.ReadPacket(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		code   string
		reason byte
	}{{"used-test", 16}, {"unknown-test", 14}} {
		request := protocol.NewWriter(protocol.RecvCashCoupon)
		request.U8(0)
		request.String(tc.code)
		request.String("")
		request.U8(0)
		if err = c.WritePacket(request.Packet()); err != nil {
			t.Fatal(err)
		}
		packet, err := c.ReadPacket()
		if err != nil || !bytes.Equal(packet, []byte{0x61, 0x02, 104, tc.reason}) {
			t.Fatalf("coupon %s failure %x: %v", tc.code, packet, err)
		}
		for _, want := range [][]byte{protocol.CashLocker(nil), protocol.CashWalletPacket(domain.CashWallet{Credit: 943}), protocol.EnableCashshop()} {
			packet, err = c.ReadPacket()
			if err != nil || !bytes.Equal(packet, want) {
				t.Fatalf("coupon changed wallet/locker: %x want%x %v", packet, want, err)
			}
		}
		select {
		case received := <-repo.requests:
			if received.Kind != "coupon" || received.Coupon != tc.code {
				t.Fatalf("wrong business command: %+v", received)
			}
		default:
			t.Fatal("coupon did not reach its business command")
		}
	}
	malformed := protocol.NewWriter(protocol.RecvCashCoupon)
	malformed.U8(9)
	if err = c.WritePacket(malformed.Packet()); err != nil {
		t.Fatal(err)
	}
	packet, err := c.ReadPacket()
	if err != nil || !bytes.Equal(packet, []byte{0x61, 0x02, 104, 14}) {
		t.Fatalf("malformed coupon notice%x %v", packet, err)
	}
	select {
	case <-repo.requests:
		t.Fatal("malformed coupon changed business state")
	default:
	}
	c.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("coupon packet test cleanup stalled")
	}
}
