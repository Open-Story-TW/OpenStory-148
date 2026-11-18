-- Preserve the origin of each Blue Aura recipient across skill refreshes.
ALTER TABLE game_skill_buffs ADD COLUMN source_character_id bigint NOT NULL DEFAULT 0 CHECK (source_character_id >= 0);

-- Native skill cancel has no client tick. The operation ID still deduplicates it.
ALTER TABLE asset_skill_casts ALTER COLUMN client_tick DROP NOT NULL;
