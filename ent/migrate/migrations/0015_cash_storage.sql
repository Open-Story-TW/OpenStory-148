ALTER TABLE asset_cash_items DROP CONSTRAINT asset_cash_items_state_check;
ALTER TABLE asset_cash_items ADD CONSTRAINT asset_cash_items_state_check CHECK(state IN('locker','inventory','storage','expired'));
ALTER TABLE asset_storage_items ADD COLUMN cash_id bigint NOT NULL DEFAULT 0;
ALTER TABLE asset_storage_items ADD COLUMN expires_at bigint NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX asset_storage_cash_serial ON asset_storage_items(cash_id) WHERE cash_id>0;
ALTER TABLE asset_storage_items DROP CONSTRAINT asset_storage_items_inv_type_check;
ALTER TABLE asset_storage_items ADD CONSTRAINT asset_storage_items_inv_type_check CHECK(inv_type BETWEEN 1 AND 5);
