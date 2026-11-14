-- Account and IP bans set by a standing GM (!ban, !banip). A banned account
-- or address is refused at login and at character select.
ALTER TABLE auth_accounts ADD COLUMN banned_until timestamptz;
ALTER TABLE auth_accounts ADD COLUMN ban_reason text NOT NULL DEFAULT '';
ALTER TABLE auth_accounts ADD COLUMN banned_by text NOT NULL DEFAULT '';

CREATE TABLE ops_ip_bans (
  ip text PRIMARY KEY,
  reason text NOT NULL DEFAULT '',
  banned_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
