CREATE TABLE asset_combat_loot_jobs (
 operation_id text PRIMARY KEY REFERENCES asset_combat_rewards(operation_id),
 owner_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 channel_id integer NOT NULL CHECK(channel_id BETWEEN 1 AND 40),
 map_id integer NOT NULL CHECK(map_id >= 0),
 mob_id integer NOT NULL CHECK(mob_id > 0),
 x integer NOT NULL CHECK(x BETWEEN -32768 AND 32767),
 y integer NOT NULL CHECK(y BETWEEN -32768 AND 32767),
 loot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 materialized_at timestamptz
);
CREATE INDEX asset_combat_loot_pending ON asset_combat_loot_jobs(world_id,created_at) WHERE materialized_at IS NULL;
