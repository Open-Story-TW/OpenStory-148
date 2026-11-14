CREATE TABLE asset_equipment_metadata (
 equipment_id bigint PRIMARY KEY,
 account_id bigint NOT NULL REFERENCES auth_accounts(id),
 metadata jsonb NOT NULL
);
CREATE INDEX asset_equipment_metadata_account ON asset_equipment_metadata(account_id);
CREATE TABLE asset_enhancement_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 session_id text NOT NULL,
 generation bigint NOT NULL,
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(character_id,session_id,generation,client_tick)
);
