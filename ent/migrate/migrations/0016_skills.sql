CREATE TABLE game_skills (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 skill_id integer NOT NULL CHECK(skill_id>0),
 level integer NOT NULL CHECK(level BETWEEN 0 AND 255),
 master_level integer NOT NULL CHECK(master_level BETWEEN 0 AND 255),
 expires_at timestamptz,
 PRIMARY KEY(character_id,skill_id)
);
CREATE TABLE game_skill_cooldowns (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 skill_id integer NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(character_id,skill_id)
);
CREATE TABLE game_skill_buffs (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 skill_id integer NOT NULL,
 level integer NOT NULL CHECK(level BETWEEN 1 AND 255),
 stat text NOT NULL,
 value integer NOT NULL,
 expires_at timestamptz NOT NULL,
 next_tick timestamptz,
 PRIMARY KEY(character_id,stat)
);
CREATE TABLE asset_skill_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN('allocate','cast')),
 skill_id integer NOT NULL,
 client_level integer NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,operation_id)
);
