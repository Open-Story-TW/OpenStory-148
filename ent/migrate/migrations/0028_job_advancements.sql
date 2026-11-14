CREATE TABLE game_job_advancements (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL CHECK(length(operation_id) BETWEEN 16 AND 128),
 source_script text NOT NULL,
 from_job integer NOT NULL CHECK(from_job >= 0),
 to_job integer NOT NULL CHECK(to_job > 0),
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(character_id,operation_id),
 UNIQUE(character_id,to_job)
);
