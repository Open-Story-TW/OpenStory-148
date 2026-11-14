-- Buff rows name their stat by the Java MapleBuffStatus name used by
-- internal/buffstat, so one skill can own several rows (Hyper Body: MAXHP and
-- MAXMP) and a corrected native bit never needs a data migration.
UPDATE game_skill_buffs SET stat=CASE stat WHEN 'wdef' THEN 'WDEF' WHEN 'speed' THEN 'SPEED' WHEN 'recovery' THEN 'RECOVERY' ELSE stat END;
DELETE FROM game_skill_buffs WHERE stat !~ '^[A-Z][A-Z0-9_]*$';
ALTER TABLE game_skill_buffs ADD CONSTRAINT game_skill_buffs_stat_name CHECK(stat ~ '^[A-Z][A-Z0-9_]*$');
CREATE INDEX game_skill_buffs_skill ON game_skill_buffs(character_id,skill_id);
