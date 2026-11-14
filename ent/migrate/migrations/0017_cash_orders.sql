CREATE TABLE asset_cash_wishlists (
 character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
 serials jsonb NOT NULL DEFAULT '[]'
);
ALTER TABLE asset_cash_items ADD COLUMN gift_from text NOT NULL DEFAULT '';
CREATE TABLE asset_cash_feature_operations (
 operation_id text PRIMARY KEY,
 account_id bigint NOT NULL REFERENCES auth_accounts(id),
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE asset_cash_gifts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 sender_account_id bigint NOT NULL REFERENCES auth_accounts(id),
 sender_name text NOT NULL,
 recipient_character_id bigint NOT NULL REFERENCES game_characters(id),
 message text NOT NULL,
 sn integer NOT NULL,
 template_id integer NOT NULL,
 products jsonb NOT NULL,
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX asset_cash_gifts_recipient ON asset_cash_gifts(recipient_character_id) WHERE delivered_at IS NULL;
CREATE TABLE asset_cash_coupons (
 code_hash text PRIMARY KEY,
 kind text NOT NULL CHECK(kind IN('credit','points','item','mesos')),
 value bigint NOT NULL CHECK(value>0),
 account_id bigint REFERENCES auth_accounts(id),
 expires_at timestamptz NOT NULL,
 redeemed_account_id bigint REFERENCES auth_accounts(id),
 redeemed_operation_id text UNIQUE,
 redeemed_at timestamptz
);
CREATE TABLE asset_cash_limits (
 sn integer PRIMARY KEY,
 total_limit bigint CHECK(total_limit>0),
 account_limit bigint CHECK(account_limit>0)
);
