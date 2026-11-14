-- Server-only counter of a buff (Owl Spirit's remaining hits): never sent to
-- the client, which receives the stat value (the damage bonus) instead.
ALTER TABLE game_skill_buffs ADD COLUMN counter integer NOT NULL DEFAULT 0 CHECK(counter BETWEEN 0 AND 1000000);
