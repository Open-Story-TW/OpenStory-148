CREATE TABLE game_inner_abilities (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 slot smallint NOT NULL CHECK(slot BETWEEN 1 AND 3),
 skill_id integer NOT NULL CHECK(skill_id BETWEEN 70000000 AND 70009999),
 level smallint NOT NULL CHECK(level BETWEEN 1 AND 40),
 grade smallint NOT NULL CHECK(grade BETWEEN 0 AND 3),
 PRIMARY KEY(character_id,slot)
);
