CREATE TABLE game_character_keymaps (
    character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
    bindings jsonb NOT NULL CHECK (jsonb_typeof(bindings)='array' AND jsonb_array_length(bindings)=89),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
