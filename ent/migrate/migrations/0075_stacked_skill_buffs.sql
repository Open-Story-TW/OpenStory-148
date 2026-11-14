-- Let one stat hold rows of several sources so MAPLEGO_STACK_BUFFS can stack
-- additive buffs. With the switch off the application still keeps one row per
-- stat, so this does not change existing behavior.
ALTER TABLE game_skill_buffs DROP CONSTRAINT game_skill_buffs_pkey;
ALTER TABLE game_skill_buffs ADD PRIMARY KEY (character_id, stat, skill_id);
