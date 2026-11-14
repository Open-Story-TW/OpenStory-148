-- Generic idempotent ledger for casts that change several characters in one
-- transaction (party buffs). Replaces the never-shipped single-skill 0040.
CREATE TABLE asset_skill_casts (
 operation_id text PRIMARY KEY,
 actor_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL,
 session_id text NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 skill_id integer NOT NULL CHECK(skill_id>0),
 payload_hash text NOT NULL CHECK(length(payload_hash)=64),
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(actor_id,session_id,generation,client_tick)
);
