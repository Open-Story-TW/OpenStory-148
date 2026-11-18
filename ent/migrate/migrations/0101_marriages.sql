CREATE TABLE game_marriage_members (
  character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
  marriage_id uuid NOT NULL,
  position smallint NOT NULL CHECK (position IN (0, 1)),
  UNIQUE(marriage_id, position)
);
