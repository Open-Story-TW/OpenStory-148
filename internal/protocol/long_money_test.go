package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestMoneyPacketsKeepInt32WithSwitchOff(t *testing.T) {
	defer domain.SetLongMoneyForTest(false)()

	if got, want := len(UpdateMesos(7, true)), 2+1+8+4+1+1; got != want {
		t.Fatalf("UpdateMesos length %d, want %d", got, want)
	}
	if UpdateMesos(int64(1)<<31, true) != nil {
		t.Fatal("a balance above int32 must not be sent while the switch is off")
	}
	if got, want := len(CashWalletPacket(domain.CashWallet{Credit: 1, Points: 2})), 2+4+4; got != want {
		t.Fatalf("CashWalletPacket length %d, want %d", got, want)
	}
	short := len(StorageMesos(domain.Storage{Mesos: 5}))
	restore := domain.SetLongMoneyForTest(true)
	long := len(StorageMesos(domain.Storage{Mesos: 5}))
	restore()
	if long != short+4 {
		t.Fatalf("StorageMesos grew by %d bytes, want 4", long-short)
	}
	if domain.MaxMoney() != 2147483647 {
		t.Fatalf("MaxMoney %d", domain.MaxMoney())
	}
}

func TestMoneyPacketsCarryInt64WithSwitchOn(t *testing.T) {
	defer domain.SetLongMoneyForTest(true)()
	const value = int64(5_000_000_000)

	r := NewReader(UpdateMesos(value, false)[2:])
	if r.U8() != 0 || r.I64() != 0x40000 || r.I64() != value || r.U8() != 0 || r.U8() != 0 || r.Remaining() != 0 {
		t.Fatal("UpdateMesos does not carry a 64-bit balance")
	}

	r = NewReader(CashWalletPacket(domain.CashWallet{Credit: value, Points: value + 1})[2:])
	if r.I64() != value || r.I64() != value+1 || r.Remaining() != 0 {
		t.Fatal("CashWalletPacket does not carry 64-bit balances")
	}

	packet := StorageMesos(domain.Storage{Mesos: value})
	r = NewReader(packet[len(packet)-8:])
	if r.I64() != value {
		t.Fatal("StorageMesos does not end with a 64-bit balance")
	}
}

func TestCharacterInfoMoneyWidthFollowsTheSwitch(t *testing.T) {
	c := domain.Character{ID: 1, Name: "money", Level: 1, Mesos: 123}
	restore := domain.SetLongMoneyForTest(false)
	short := len(CashOpen(c))
	restore()
	defer domain.SetLongMoneyForTest(true)()
	if got := len(CashOpen(c)); got != short+4 {
		t.Fatalf("character info grew by %d bytes, want 4", got-short)
	}
}

func TestTradeOfferAndStorageTransferWidthFollowsTheSwitch(t *testing.T) {
	const value = int64(5_000_000_000)
	restore := domain.SetLongMoneyForTest(false)
	if TradeMesos(0, value) != nil {
		t.Fatal("an offer above int32 must not be sent while the switch is off")
	}
	if got, want := len(TradeMesos(1, 7)), 2+1+1+4; got != want {
		t.Fatalf("TradeMesos length %d, want %d", got, want)
	}
	if got := ReadMoney(NewReader([]byte{7, 0, 0, 0})); got != 7 {
		t.Fatalf("ReadMoney %d", got)
	}
	restore()

	defer domain.SetLongMoneyForTest(true)()
	r := NewReader(TradeMesos(1, value)[2:])
	if r.U8() != 1 || r.U8() != 1 || r.I64() != value || r.Remaining() != 0 {
		t.Fatal("TradeMesos does not carry a 64-bit offer")
	}
	request, err := ReadTradeRequest(NewReader([]byte{1, 0x00, 0xf2, 0x05, 0x2a, 0x01, 0, 0, 0}))
	if err != nil || request.Mesos != value {
		t.Fatalf("trade offer request: %v %d", err, request.Mesos)
	}
	// A storage withdrawal is positive and a deposit negative.
	if got := ReadMoney(NewReader([]byte{0x00, 0x0e, 0xfa, 0xd5, 0xfe, 0xff, 0xff, 0xff})); got != -value {
		t.Fatalf("ReadMoney %d, want %d", got, -value)
	}
}
