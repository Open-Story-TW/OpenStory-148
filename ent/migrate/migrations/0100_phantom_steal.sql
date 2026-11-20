CREATE TABLE IF NOT EXISTS game_phantom_stolen_skills (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 tier smallint NOT NULL CHECK(tier BETWEEN 1 AND 4),
 slot smallint NOT NULL CHECK(slot BETWEEN 0 AND 3),
 skill_id integer NOT NULL CHECK(skill_id > 0),
 level smallint NOT NULL CHECK(level BETWEEN 1 AND 255),
 master_level smallint NOT NULL DEFAULT 0 CHECK(master_level BETWEEN 0 AND 255),
 PRIMARY KEY(character_id, tier, slot)
);

CREATE TABLE IF NOT EXISTS game_phantom_chosen_skills (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 base_skill_id integer NOT NULL CHECK(base_skill_id IN (24001001, 24101001, 24111001, 24121001)),
 chosen_skill_id integer NOT NULL CHECK(chosen_skill_id > 0),
 PRIMARY KEY(character_id, base_skill_id)
);
