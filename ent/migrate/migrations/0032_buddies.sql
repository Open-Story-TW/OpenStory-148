CREATE TABLE game_buddy_profiles (
    character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
    capacity integer NOT NULL DEFAULT 232 CHECK (capacity BETWEEN 1 AND 255)
);

-- Directed entries retain each player's group and pending/accepted state.
-- Online channel is deliberately absent: it is derived from live ownership.
CREATE TABLE game_buddies (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    buddy_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    group_name text NOT NULL,
    pending boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (character_id, buddy_id),
    CHECK (character_id <> buddy_id)
);
CREATE INDEX game_buddies_target_idx ON game_buddies (buddy_id, character_id);
