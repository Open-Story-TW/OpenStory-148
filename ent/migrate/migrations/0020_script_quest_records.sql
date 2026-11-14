CREATE TABLE game_script_quest_records (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 quest_id integer NOT NULL CHECK(quest_id > 0),
 data text NOT NULL DEFAULT '' CHECK(octet_length(data) <= 4096),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision > 0),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,quest_id)
);
