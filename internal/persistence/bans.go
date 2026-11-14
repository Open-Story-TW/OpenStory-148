package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
	"time"

	"maple-go-server/internal/domain"
)

// Bans are set by standing GM commands, which the GM audit already records.
// A ban names the account through one of its characters.

// LoginBanned reports whether the account or the address may not log in.
func (s *Store) LoginBanned(ctx context.Context, accountID int64, ip string) (bool, error) {
	return banQuery(ctx, s.db, accountID, ip)
}

func banQuery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID int64, ip string) (bool, error) {
	var banned bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_accounts WHERE id=$1 AND banned_until>clock_timestamp()) OR EXISTS(SELECT 1 FROM ops_ip_bans WHERE ip=$2)`, accountID, ip).Scan(&banned)
	return banned, err
}

// BanAccount bans the account that owns the named character until the given
// time. It returns the account's characters so the world can disconnect them.
func (s *Store) BanAccount(ctx context.Context, character string, until time.Time, reason, actor string) (domain.GMBan, error) {
	var ban domain.GMBan
	if character == "" || len(actor) < 1 || len(actor) > 128 || len(reason) > 255 {
		return ban, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ban, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `UPDATE auth_accounts SET banned_until=$2,ban_reason=$3,banned_by=$4 WHERE id=(SELECT account_id FROM game_characters WHERE name_key=lower($1)) RETURNING id,name`, character, until, reason, actor).Scan(&ban.AccountID, &ban.Account)
	if errors.Is(err, sql.ErrNoRows) {
		return ban, domain.ErrNotFound
	}
	if err != nil {
		return ban, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM game_characters WHERE account_id=$1`, ban.AccountID)
	if err != nil {
		return ban, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return ban, err
		}
		ban.CharacterIDs = append(ban.CharacterIDs, id)
	}
	if err = rows.Err(); err != nil {
		return ban, err
	}
	ban.Until = until
	return ban, tx.Commit()
}

// UnbanAccount lifts the ban of the account that owns the named character.
func (s *Store) UnbanAccount(ctx context.Context, character string) (domain.GMBan, error) {
	var ban domain.GMBan
	err := s.db.QueryRowContext(ctx, `UPDATE auth_accounts SET banned_until=NULL,ban_reason='',banned_by='' WHERE id=(SELECT account_id FROM game_characters WHERE name_key=lower($1)) RETURNING id,name`, character).Scan(&ban.AccountID, &ban.Account)
	if errors.Is(err, sql.ErrNoRows) {
		return ban, domain.ErrNotFound
	}
	return ban, err
}

// LastLoginIP is the address the named character last entered the world from.
func (s *Store) LastLoginIP(ctx context.Context, character string) (string, error) {
	var ip string
	err := s.db.QueryRowContext(ctx, `SELECT t.client_ip FROM ops_transfers t JOIN game_characters c ON c.id=t.character_id WHERE c.name_key=lower($1) ORDER BY t.expires_at DESC LIMIT 1`, character).Scan(&ip)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return ip, err
}

// BanIP refuses every login from one address. Loopback is never banned: it is
// where the operator's own tools connect from.
func (s *Store) BanIP(ctx context.Context, ip, reason, actor string) error {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.IsLoopback() || len(actor) < 1 || len(actor) > 128 || len(reason) > 255 {
		return domain.ErrInvalidInput
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO ops_ip_bans(ip,reason,banned_by) VALUES($1,$2,$3) ON CONFLICT(ip) DO UPDATE SET reason=excluded.reason,banned_by=excluded.banned_by`, parsed.String(), reason, actor)
	return err
}

// UnbanIP lifts an address ban; ErrNotFound when the address was not banned.
func (s *Store) UnbanIP(ctx context.Context, ip string) error {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return domain.ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM ops_ip_bans WHERE ip=$1`, parsed.String())
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
