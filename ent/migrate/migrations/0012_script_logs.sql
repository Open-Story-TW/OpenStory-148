-- Separate calendar buckets retain history without destructive midnight resets.
CREATE TABLE game_script_logs (
 scope text NOT NULL CHECK(scope IN ('character','account','world')),
 owner_id bigint NOT NULL CHECK(owner_id >= 0),
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 period text NOT NULL CHECK(period IN ('permanent','hour','day','week','month','year')),
 window_start timestamptz NOT NULL,
 value bigint NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision > 0),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(scope,owner_id,world_id,name,period,window_start)
);
CREATE TABLE asset_script_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL,
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,operation_id)
);
