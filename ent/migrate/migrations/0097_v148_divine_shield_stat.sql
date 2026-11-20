-- Older builds stored Divine Shield using its former DICE_ROLL alias.
DELETE FROM game_skill_buffs AS old
USING game_skill_buffs AS current
WHERE old.character_id = current.character_id
  AND old.skill_id = 1220013 AND current.skill_id = old.skill_id
  AND old.stat = 'DICE_ROLL' AND current.stat = 'DIVINE_SHIELD';

UPDATE game_skill_buffs
SET stat = 'DIVINE_SHIELD'
WHERE skill_id = 1220013 AND stat = 'DICE_ROLL';
