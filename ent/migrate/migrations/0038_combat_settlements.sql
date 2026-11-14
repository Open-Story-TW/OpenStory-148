CREATE TABLE asset_combat_attacks (
 operation_id text PRIMARY KEY,
 actor_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 session_id text NOT NULL,
 generation bigint NOT NULL,
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 family integer NOT NULL CHECK(family BETWEEN 0 AND 2),
 payload_hash text NOT NULL,
 manifest jsonb NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(actor_id,session_id,generation,client_tick)
);
CREATE TABLE asset_combat_settlements (
 spawn_operation_id text PRIMARY KEY REFERENCES asset_combat_rewards(operation_id) ON DELETE CASCADE,
 attack_operation_id text NOT NULL REFERENCES asset_combat_attacks(operation_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 mob_id integer NOT NULL CHECK(mob_id>0),
 manifest jsonb NOT NULL
);
CREATE TABLE asset_combat_grants (
 spawn_operation_id text NOT NULL REFERENCES asset_combat_settlements(spawn_operation_id) ON DELETE CASCADE,
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 experience bigint NOT NULL CHECK(experience>=0),
 base_experience bigint NOT NULL CHECK(base_experience>=0),
 party_experience bigint NOT NULL CHECK(party_experience>=0),
 character_revision bigint NOT NULL,
 PRIMARY KEY(spawn_operation_id,character_id)
);
