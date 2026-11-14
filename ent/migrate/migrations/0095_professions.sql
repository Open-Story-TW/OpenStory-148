-- Professions (Skill.wz 9200-9204): level and proficiency per learned
-- profession, recipes learned from recipe items, the six personality traits
-- and the fatigue that gathering and crafting build up.
CREATE TABLE game_professions (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 skill_id integer NOT NULL CHECK(skill_id IN(92000000,92010000,92020000,92030000,92040000)),
 level integer NOT NULL DEFAULT 1 CHECK(level BETWEEN 1 AND 10),
 exp integer NOT NULL DEFAULT 0 CHECK(exp>=0),
 PRIMARY KEY(character_id,skill_id)
);
CREATE TABLE game_profession_recipes (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 recipe_id integer NOT NULL CHECK(recipe_id BETWEEN 92000001 AND 92049999),
 PRIMARY KEY(character_id,recipe_id)
);
-- fatigue is the value at fatigue_at; it wears off afterwards.
CREATE TABLE game_character_traits (
 character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
 charisma integer NOT NULL DEFAULT 0 CHECK(charisma>=0),
 insight integer NOT NULL DEFAULT 0 CHECK(insight>=0),
 will integer NOT NULL DEFAULT 0 CHECK(will>=0),
 craft integer NOT NULL DEFAULT 0 CHECK(craft>=0),
 sense integer NOT NULL DEFAULT 0 CHECK(sense>=0),
 charm integer NOT NULL DEFAULT 0 CHECK(charm>=0),
 fatigue integer NOT NULL DEFAULT 0 CHECK(fatigue BETWEEN 0 AND 200),
 fatigue_at timestamptz NOT NULL DEFAULT now()
);
