-- S0b: generic registered-skill attack family (3) shares the combat receipt.
ALTER TABLE asset_combat_attacks DROP CONSTRAINT IF EXISTS asset_combat_attacks_family_check;
ALTER TABLE asset_combat_attacks ADD CONSTRAINT asset_combat_attacks_family_check CHECK(family BETWEEN 0 AND 3);
