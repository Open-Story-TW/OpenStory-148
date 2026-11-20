CREATE TABLE game_inner_ability_progress (
 character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
 honor_level integer NOT NULL CHECK(honor_level >= 0),
 honor_exp integer NOT NULL CHECK(honor_exp >= 0)
);

INSERT INTO game_inner_ability_progress(character_id,honor_level,honor_exp)
 SELECT DISTINCT character_id,0,0 FROM game_inner_abilities;

ALTER TABLE game_inner_abilities ADD CONSTRAINT game_inner_abilities_progress_fk
 FOREIGN KEY(character_id) REFERENCES game_inner_ability_progress(character_id) ON DELETE CASCADE;
