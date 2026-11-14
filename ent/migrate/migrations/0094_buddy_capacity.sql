-- Large friend lists: a stored capacity is no longer capped at the native
-- one-byte 255. The operator's buddy_capacity (at most 1000) is applied when
-- the list is used.
ALTER TABLE game_buddy_profiles DROP CONSTRAINT IF EXISTS game_buddy_profiles_capacity_check;
ALTER TABLE game_buddy_profiles ADD CONSTRAINT game_buddy_profiles_capacity_check CHECK (capacity BETWEEN 1 AND 32767);
