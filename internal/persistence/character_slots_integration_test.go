package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

// Creation stops at the limit; lowering the limit later keeps every existing
// character listed and selectable and only blocks new ones.
func TestCharacterSlotLimitBlocksCreationOnly(t *testing.T) {
	usePrivateSchema(t, "slots")
	defer domain.SetCharacterSlotsForTest(3)()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	const world = 122
	lease, err := s.AcquireWorldLease(ctx, world, "slots-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseWorldLease(context.Background(), lease) })
	account, err := s.Register(ctx, "sl"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.roster.Invalidate(context.Background(), account.ID, world) })
	if err = s.ConfigureAccount(ctx, account.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := s.BeginLogin(ctx, account.ID, "slots-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	create := func(n int) (domain.Character, error) {
		return s.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: account.ID, WorldID: world, Name: fmt.Sprintf("Sl%s%d", suffix, n), Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: fmt.Sprintf("slots-create-%s-%d", suffix, n)})
	}
	var ids []int64
	for n := range 3 {
		c, err := create(n)
		if err != nil {
			t.Fatalf("character %d: %v", n, err)
		}
		ids = append(ids, c.ID)
	}
	if _, err = create(3); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("fourth character under a limit of three: %v", err)
	}
	domain.SetCharacterSlots(1)
	if _, err = create(4); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("creation above a lowered limit: %v", err)
	}
	s.roster.Invalidate(ctx, account.ID, world)
	list, err := s.ListCharacters(ctx, account.ID, world)
	if err != nil || len(list) != 3 {
		t.Fatalf("list after lowering the limit: %d %v", len(list), err)
	}
	if got := domain.AdvertisedCharacterSlots(len(list)); got != 3 {
		t.Fatalf("advertised %d slots", got)
	}
	domain.SetCharacterSlots(4)
	if _, err = create(5); err != nil {
		t.Fatalf("creation after raising the limit: %v", err)
	}
	domain.SetCharacterSlots(1)
	if _, err = s.CreateTransfer(ctx, domain.TransferRequest{AccountID: account.ID, CharacterID: ids[2], WorldID: world, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"}); err != nil {
		t.Fatalf("select a character above the lowered limit: %v", err)
	}
}
