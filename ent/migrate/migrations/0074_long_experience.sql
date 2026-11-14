-- Let the application's MAPLEGO_LONG_EXPERIENCE switch decide the EXP ceiling.
-- The column is already bigint; with the switch off the application still stops
-- at the 32-bit ceiling, so this does not change existing behavior.
ALTER TABLE game_characters DROP CONSTRAINT IF EXISTS game_characters_exp_check;
ALTER TABLE game_characters ADD CONSTRAINT game_characters_exp_check CHECK (exp >= 0);
