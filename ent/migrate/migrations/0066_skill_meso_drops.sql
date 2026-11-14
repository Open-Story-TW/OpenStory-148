-- S0b: skills that consume ground mesos (Meso Explosion) keep one receipt
-- per operation; consumed drops are retired via expired_at.
CREATE TABLE asset_meso_consumptions (
 operation_id text PRIMARY KEY CHECK(length(operation_id) BETWEEN 16 AND 200),
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 skill_id integer NOT NULL CHECK(skill_id > 0),
 object_ids jsonb NOT NULL,
 total bigint NOT NULL CHECK(total >= 0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- Skill-created meso drops (Pickpocket) are idempotent by their own key; the
-- player-drop source_operation_id references the balance ledger instead.
ALTER TABLE asset_item_drops ADD COLUMN skill_operation_id text UNIQUE;
