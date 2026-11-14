-- Operator-editable server settings and the web admin's own sign-in state.
-- A missing ops_server_settings row means "use the environment default".
CREATE TABLE ops_server_settings (
 key text PRIMARY KEY CHECK(length(key) BETWEEN 1 AND 64),
 value text NOT NULL CHECK(length(value)<=1024),
 updated_by text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE ops_admin_users (
 email text PRIMARY KEY CHECK(email=lower(email) AND length(email) BETWEEN 3 AND 254),
 added_by text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- One pending sign-in code per address; only its hash is stored.
CREATE TABLE ops_admin_login_codes (
 email text PRIMARY KEY,
 code_hash text NOT NULL CHECK(length(code_hash)=64),
 attempts integer NOT NULL DEFAULT 0,
 sent_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL
);
CREATE TABLE ops_admin_sessions (
 token_hash text PRIMARY KEY CHECK(length(token_hash)=64),
 email text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX ops_admin_sessions_expiry ON ops_admin_sessions(expires_at);
