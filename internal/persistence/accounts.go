package persistence

import (
	"context"
	"maple-go-server/ent"
	"maple-go-server/ent/account"
	"maple-go-server/internal/domain"
	"regexp"
	"strings"
)

var accountPattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,24}$`)
var pinPattern = regexp.MustCompile(`^[0-9]{4,16}$`)

func accountValue(a *ent.Account) domain.Account {
	return domain.Account{ID: a.ID, Name: a.Name, Gender: a.Gender, PINConfigured: a.PinHash != ""}
}
func (s *Store) Register(ctx context.Context, name, password string) (domain.Account, error) {
	if !accountPattern.MatchString(name) || len(password) < 6 || len(password) > 128 {
		return domain.Account{}, domain.ErrInvalidInput
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return domain.Account{}, err
	}
	a, err := s.client.Account.Create().SetName(name).SetNameKey(strings.ToLower(name)).SetPasswordHash(hash).Save(ctx)
	if ent.IsConstraintError(err) {
		return domain.Account{}, domain.ErrAlreadyExists
	}
	if err != nil {
		return domain.Account{}, err
	}
	return accountValue(a), nil
}
func (s *Store) Authenticate(ctx context.Context, name, password string) (domain.Account, error) {
	if len(name) > 24 || len(password) > 128 {
		return domain.Account{}, domain.ErrBadCredentials
	}
	a, err := s.client.Account.Query().Where(account.NameKeyEQ(strings.ToLower(name))).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	if !verifyPassword(ctx, a.PasswordHash, password) {
		return domain.Account{}, domain.ErrBadCredentials
	}
	return accountValue(a), nil
}
func (s *Store) ConfigureAccount(ctx context.Context, id int64, gender int, pin string) error {
	if (gender != 0 && gender != 1) || !pinPattern.MatchString(pin) {
		return domain.ErrInvalidInput
	}
	hash, err := hashPassword(ctx, pin)
	if err != nil {
		return err
	}
	// Setup is one-way. Existing PIN changes need a separate authenticated flow.
	changed, err := s.client.Account.Update().Where(account.IDEQ(id), account.GenderEQ(255), account.PinHashEQ("")).SetGender(gender).SetPinHash(hash).Save(ctx)
	if err != nil {
		return err
	}
	if changed != 1 {
		return domain.ErrForbidden
	}
	return nil
}
func (s *Store) VerifyPIN(ctx context.Context, id int64, pin string) error {
	a, err := s.client.Account.Get(ctx, id)
	if err != nil {
		return domain.ErrBadCredentials
	}
	if !verifyPassword(ctx, a.PinHash, pin) {
		return domain.ErrBadCredentials
	}
	return nil
}
