-- Large guilds: capacity is no longer capped at Legacy's 200. The operator's
-- ceiling (guild_max_capacity, at most 700) is applied when a guild is read,
-- so the column only bounds what expansion may store.
ALTER TABLE game_guilds DROP CONSTRAINT IF EXISTS game_guilds_capacity_check;
ALTER TABLE game_guilds ADD CONSTRAINT game_guilds_capacity_check CHECK (capacity BETWEEN 1 AND 32767);
