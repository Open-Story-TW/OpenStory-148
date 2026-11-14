ALTER TABLE asset_item_drops
 ADD COLUMN player_drop boolean NOT NULL DEFAULT false,
 ADD COLUMN source_operation_id text UNIQUE REFERENCES asset_meso_ledger(operation_id);
