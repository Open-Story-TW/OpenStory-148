-- Only the login projection is reduced. Authoritative game_* / asset_* rows
-- retain all gameplay data, and the projection revision remains unchanged.
UPDATE game_character_roster r
SET snapshot=jsonb_set(
 r.snapshot - ARRAY['items','quests','skills','skill_cooldowns','skill_buffs'],
 '{equipment}',
 COALESCE((SELECT jsonb_agg(e) FROM jsonb_array_elements(
  CASE WHEN jsonb_typeof(r.snapshot->'equipment')='array' THEN r.snapshot->'equipment' ELSE '[]'::jsonb END
 ) e WHERE (e->>'slot')::integer < 0), '[]'::jsonb)
);
