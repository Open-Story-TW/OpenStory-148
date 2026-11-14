ALTER TABLE asset_cash_moves
 ADD COLUMN inventory_type smallint NOT NULL DEFAULT 0 CHECK(inventory_type BETWEEN 0 AND 5),
 ADD COLUMN target_slot smallint NOT NULL DEFAULT 0 CHECK(target_slot BETWEEN 0 AND 96);
