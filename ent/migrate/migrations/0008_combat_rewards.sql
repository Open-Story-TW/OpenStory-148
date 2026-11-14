CREATE TABLE asset_combat_rewards (
 operation_id text PRIMARY KEY,
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 experience bigint NOT NULL CHECK(experience >= 0),
 character_revision bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_combat_rewards_character ON asset_combat_rewards(character_id);
