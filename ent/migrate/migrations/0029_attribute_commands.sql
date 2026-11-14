CREATE TABLE asset_attribute_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 session_id text NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 stat_mask bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(character_id,session_id,generation,client_tick)
);
