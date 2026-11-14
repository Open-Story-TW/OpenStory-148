-- Preserve the complete native automatic-AP intent under the same tick key
-- as single-point allocations, so neither replay nor a changed payload spends AP.
ALTER TABLE asset_attribute_commands
 ADD COLUMN allocations jsonb NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(allocations) = 'array');
