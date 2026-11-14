CREATE TABLE asset_shop_operations (
 operation_id text PRIMARY KEY,
 account_id bigint NOT NULL REFERENCES auth_accounts(id),
 world_id integer NOT NULL,
 character_id bigint NOT NULL REFERENCES game_characters(id),
 shop_id integer NOT NULL,
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
