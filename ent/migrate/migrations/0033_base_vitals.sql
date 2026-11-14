ALTER TABLE game_characters ADD COLUMN base_max_hp integer DEFAULT 50;
ALTER TABLE game_characters ADD COLUMN base_max_mp integer DEFAULT 5;
ALTER TABLE game_characters ADD COLUMN vitals_model integer NOT NULL DEFAULT 1;
ALTER TABLE game_characters ADD COLUMN vitals_origin text NOT NULL DEFAULT 'explicit_go_v1';

-- Compatibility reconstruction only: historical level-up caps may have lost
-- the true raw base. Preserve the complete pre-migration inputs and policy.
CREATE TABLE ops_vitals_backfill (
 character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
 character_revision bigint NOT NULL,
 old_hp integer NOT NULL, old_mp integer NOT NULL,
 old_max_hp integer NOT NULL, old_max_mp integer NOT NULL,
 equipped_hp bigint NOT NULL, equipped_mp bigint NOT NULL,
 inferred_base_hp bigint NOT NULL, inferred_base_mp bigint NOT NULL,
 policy text NOT NULL, status text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_vitals_backfill(character_id,character_revision,old_hp,old_mp,old_max_hp,old_max_mp,equipped_hp,equipped_mp,inferred_base_hp,inferred_base_mp,policy,status)
SELECT c.id,c.revision,c.hp,c.mp,c.max_hp,c.max_mp,COALESCE(e.hp,0),COALESCE(e.mp,0),c.max_hp::bigint-COALESCE(e.hp,0),c.max_mp::bigint-COALESCE(e.mp,0),
 'legacy_effective_minus_worn_slots_-199_to_-1_v1',
 CASE WHEN c.max_hp BETWEEN 1 AND 99999 AND c.max_mp BETWEEN 0 AND 99999 AND c.max_hp::bigint-COALESCE(e.hp,0) BETWEEN 1 AND 99999 AND c.max_mp::bigint-COALESCE(e.mp,0) BETWEEN 0 AND 99999 THEN 'compatibility_inferred' ELSE 'ambiguous_preserved' END
FROM game_characters c LEFT JOIN (SELECT character_id,SUM(hp)::bigint hp,SUM(mp)::bigint mp FROM asset_equipment WHERE slot<0 AND slot>=-199 GROUP BY character_id)e ON e.character_id=c.id;
UPDATE game_characters c SET
 base_max_hp=CASE WHEN a.status='compatibility_inferred' THEN a.inferred_base_hp::integer ELSE NULL END,
 base_max_mp=CASE WHEN a.status='compatibility_inferred' THEN a.inferred_base_mp::integer ELSE NULL END,
 vitals_model=CASE WHEN a.status='compatibility_inferred' THEN 1 ELSE 0 END,
 vitals_origin=CASE WHEN a.status='compatibility_inferred' THEN 'legacy_compatibility_v1' ELSE 'legacy_ambiguous_preserved_v1' END
FROM ops_vitals_backfill a WHERE a.character_id=c.id;
-- Existing effective/current vitals stay untouched, including ambiguous rows.
UPDATE game_character_roster r SET snapshot=r.snapshot || jsonb_build_object('base_max_hp',c.base_max_hp,'base_max_mp',c.base_max_mp,'vitals_model',c.vitals_model,'vitals_origin',c.vitals_origin)
FROM game_characters c WHERE c.id=r.character_id;
ALTER TABLE game_characters ADD CONSTRAINT game_characters_base_vitals_model CHECK(
 (vitals_model=0 AND base_max_hp IS NULL AND base_max_mp IS NULL) OR
 (vitals_model=1 AND base_max_hp IS NOT NULL AND base_max_mp IS NOT NULL AND base_max_hp BETWEEN 1 AND 99999 AND base_max_mp BETWEEN 0 AND 99999)
);
