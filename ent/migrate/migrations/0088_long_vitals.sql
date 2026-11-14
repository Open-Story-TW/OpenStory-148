-- Max HP and MP up to 9,999,999 (MAPLEGO_LONG_VITALS). Only the base maxima
-- had a 99,999 CHECK; the effective and current columns were never bounded.
ALTER TABLE game_characters DROP CONSTRAINT game_characters_base_vitals_model;
ALTER TABLE game_characters ADD CONSTRAINT game_characters_base_vitals_model CHECK(
 (vitals_model=0 AND base_max_hp IS NULL AND base_max_mp IS NULL) OR
 (vitals_model=1 AND base_max_hp IS NOT NULL AND base_max_mp IS NOT NULL AND base_max_hp BETWEEN 1 AND 9999999 AND base_max_mp BETWEEN 0 AND 9999999)
);
